package server

import (
	"bytes"
	"math"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

// item_entity.go models dropped items ("minecraft:item" entities): a resource
// popped out of a BedWars generator, lying on the ground until a player walks
// over it. Each instance simulates its items on the tick loop — a short
// gravity fall onto the block below, a vanilla-style pickup sweep around every
// living player, merging of same-item drops that land close together, and a
// despawn timer — and mirrors every change to the clients (Spawn Entity +
// metadata, Teleport Entity, Pickup Item, Remove Entities).

// Item entity type id (protocol 763), from minecraft-data.
const itemEntityTypeID int32 = 54

// Item physics / pickup tuning.
const (
	itemPopVelocity    = 0.2  // initial upward hop when a drop spawns (blocks/tick)
	itemGravity        = 0.04 // subtracted from vy each tick while airborne
	itemDrag           = 0.98 // vertical velocity retained each tick
	itemPickupDelay    = 10   // ticks before a fresh drop can be collected (shows the hop)
	itemDespawnTicks   = 6000 // 5 minutes on the ground, like vanilla
	itemMergeRadius    = 1.0  // same-item drops within this distance merge into one stack
	itemPickupRadiusXZ = 1.25 // horizontal reach of the pickup sweep from the player's centre
	itemPickupBelow    = 0.5  // how far below the feet a drop is still collected
	itemPickupAbove    = 2.25 // how far above the feet (player box 1.8 + 0.5 reach)
)

// itemEntity is one dropped stack lying in (or falling through) an instance.
type itemEntity struct {
	eid      int32
	uuid     [16]byte
	itemID   int32
	count    int
	x, y, z  float64
	vy       float64
	onGround bool
	ticks    int // age in ticks (pickup delay + despawn)
}

// DropItem spawns count of the namespaced item at (x, y, z) as a dropped-item
// entity, broadcasting it to everyone in the instance. A same-item drop that
// is already lying within itemMergeRadius absorbs the new units instead (up
// to a full stack), so a generator that fires faster than players collect
// grows one stack rather than littering entities. Returns false for an
// unknown item id or a non-positive count.
func (i *Instance) DropItem(x, y, z float64, itemName string, count int) bool {
	if count <= 0 || i.Server == nil {
		return false
	}
	itemID, ok := world.ItemByName(itemName)
	if !ok {
		return false
	}

	i.itemsMu.Lock()
	// Merge into a nearby stack of the same item first (airborne or not — the
	// distance check already bounds how far apart they can be).
	for _, it := range i.items {
		if it.itemID != itemID || it.count+count > maxStackSize {
			continue
		}
		if distXZ(it.x, it.z, x, z) <= itemMergeRadius && math.Abs(it.y-y) <= itemMergeRadius {
			it.count += count
			it.ticks = 0 // a topped-up stack is "fresh" again for despawn purposes
			meta := itemMetadataPayload(it)
			i.itemsMu.Unlock()
			i.Players.Broadcast(CbPlaySetEntityMetadata, meta, -1)
			return true
		}
	}
	it := &itemEntity{
		eid:    i.Server.nextEntityID.Add(1),
		itemID: itemID,
		count:  count,
		x:      x,
		y:      y,
		z:      z,
		vy:     itemPopVelocity,
	}
	it.uuid = entityUUID(it.eid)
	i.items = append(i.items, it)
	spawn, meta := spawnItemPayload(it), itemMetadataPayload(it)
	i.itemsMu.Unlock()

	i.Players.Broadcast(CbPlaySpawnEntity, spawn, -1)
	i.Players.Broadcast(CbPlaySetEntityMetadata, meta, -1)
	return true
}

// DroppedItemsNear counts the units of itemName lying within radius (in the
// horizontal plane, and within radius vertically) of (x, y, z). Generators use
// it to cap how much can pile up at a spawn point while nobody collects.
func (i *Instance) DroppedItemsNear(x, y, z, radius float64, itemName string) int {
	itemID, ok := world.ItemByName(itemName)
	if !ok {
		return 0
	}
	i.itemsMu.Lock()
	defer i.itemsMu.Unlock()
	total := 0
	for _, it := range i.items {
		if it.itemID == itemID && distXZ(it.x, it.z, x, z) <= radius && math.Abs(it.y-y) <= radius {
			total += it.count
		}
	}
	return total
}

// itemTick advances every dropped item one step: airborne ones fall until they
// land on a block, resting ones wait to be picked up or despawn. Registered
// on every instance in NewInstance.
func (i *Instance) itemTick(uint64) {
	i.itemsMu.Lock()
	if len(i.items) == 0 {
		i.itemsMu.Unlock()
		return
	}

	type pickup struct {
		it        *itemEntity
		collector *ClientConnection
		taken     int
		remaining int
	}
	var (
		moved   []*itemEntity
		removed []int32
		pickups []pickup
		players = i.Players.snapshot()
		kept    = i.items[:0]
	)
	for _, it := range i.items {
		it.ticks++
		if !it.onGround {
			it.vy = it.vy*itemDrag - itemGravity
			ny := it.y + it.vy
			if it.vy < 0 && i.solidAt(it.x, ny, it.z) {
				it.y = math.Floor(ny) + 1 // rest on top of the block it hit
				it.vy = 0
				it.onGround = true
			} else {
				it.y = ny
			}
			moved = append(moved, it)
			if it.y < -64 { // fell out of the world
				removed = append(removed, it.eid)
				continue
			}
		}
		if it.ticks >= itemDespawnTicks {
			removed = append(removed, it.eid)
			continue
		}
		if it.ticks >= itemPickupDelay {
			if c := i.collectorFor(it, players); c != nil {
				remaining := c.giveItem(it.itemID, it.count)
				if taken := it.count - remaining; taken > 0 {
					pickups = append(pickups, pickup{it: it, collector: c, taken: taken, remaining: remaining})
					it.count = remaining
					if remaining == 0 {
						continue // fully collected — entity goes away
					}
				}
			}
		}
		kept = append(kept, it)
	}
	for j := len(kept); j < len(i.items); j++ {
		i.items[j] = nil
	}
	i.items = kept

	// Build every payload under the lock, send after.
	type outPkt struct {
		id      int32
		payload []byte
	}
	var out []outPkt
	for _, it := range moved {
		out = append(out, outPkt{CbPlayTeleportEntity, itemTeleportPayload(it)})
	}
	for _, p := range pickups {
		out = append(out, outPkt{CbPlayPickupItem, pickupItemPayload(p.it.eid, p.collector.player.EntityID, p.taken)})
		if p.remaining > 0 {
			out = append(out, outPkt{CbPlaySetEntityMetadata, itemMetadataPayload(p.it)})
		} else {
			removed = append(removed, p.it.eid)
		}
	}
	if len(removed) > 0 {
		out = append(out, outPkt{CbPlayRemoveEntities, removeEntitiesPayload(removed)})
	}
	i.itemsMu.Unlock()

	for _, p := range out {
		i.Players.Broadcast(p.id, p.payload, -1)
	}
}

// collectorFor returns the first living, non-spectator player whose pickup
// box (vanilla: the player's hitbox grown by 1 horizontally, 0.5 vertically)
// contains the item, or nil.
func (i *Instance) collectorFor(it *itemEntity, players []*ClientConnection) *ClientConnection {
	for _, c := range players {
		if c.player == nil || c.isClosed() {
			continue
		}
		s := c.player.Snapshot()
		if s.Dead || s.Gamemode == 3 /* spectator */ {
			continue
		}
		if distXZ(s.X, s.Z, it.x, it.z) > itemPickupRadiusXZ {
			continue
		}
		if it.y < s.Y-itemPickupBelow || it.y > s.Y+itemPickupAbove {
			continue
		}
		return c
	}
	return nil
}

// distXZ is the horizontal distance between two points.
func distXZ(x1, z1, x2, z2 float64) float64 {
	return math.Hypot(x1-x2, z1-z2)
}

// sendItemEntities spawns every dropped item currently in the instance to this
// client. Called from sendWorldEntities on join and after every Respawn.
func (c *ClientConnection) sendItemEntities() error {
	type outPkt struct {
		id      int32
		payload []byte
	}
	c.instance.itemsMu.Lock()
	pkts := make([]outPkt, 0, 2*len(c.instance.items))
	for _, it := range c.instance.items {
		pkts = append(pkts,
			outPkt{CbPlaySpawnEntity, spawnItemPayload(it)},
			outPkt{CbPlaySetEntityMetadata, itemMetadataPayload(it)})
	}
	c.instance.itemsMu.Unlock()

	for _, p := range pkts {
		if err := c.safeWrite(p.id, p.payload); err != nil {
			return err
		}
	}
	return nil
}

// spawnItemPayload builds Spawn Entity (0x01) for a dropped item, with its
// current vertical velocity so a fresh drop visibly hops.
func spawnItemPayload(it *itemEntity) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, it.eid)
	buf.Write(it.uuid[:])
	protocol.WriteVarInt32ToBuffer(&buf, itemEntityTypeID)
	buf.Write(protocol.WriteDouble(it.x))
	buf.Write(protocol.WriteDouble(it.y))
	buf.Write(protocol.WriteDouble(it.z))
	buf.WriteByte(0)                        // pitch
	buf.WriteByte(0)                        // yaw
	buf.WriteByte(0)                        // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, 0) // data (unused for items)
	buf.Write(protocol.WriteShort(0))
	buf.Write(protocol.WriteShort(velocityShort(it.vy)))
	buf.Write(protocol.WriteShort(0))
	return buf.Bytes()
}

// itemMetadataPayload builds Set Entity Metadata (0x52) carrying the item
// stack (index 8, type Slot) — without it the client renders nothing.
func itemMetadataPayload(it *itemEntity) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, it.eid)
	buf.WriteByte(8)                        // index: Item
	protocol.WriteVarInt32ToBuffer(&buf, 7) // type: Slot
	buf.Write(protocol.WriteSlot(it.itemID, byte(it.count)))
	buf.WriteByte(0xFF) // end of metadata
	return buf.Bytes()
}

// itemTeleportPayload builds Teleport Entity (0x68) for an item's position.
func itemTeleportPayload(it *itemEntity) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, it.eid)
	buf.Write(protocol.WriteDouble(it.x))
	buf.Write(protocol.WriteDouble(it.y))
	buf.Write(protocol.WriteDouble(it.z))
	buf.WriteByte(0) // yaw
	buf.WriteByte(0) // pitch
	if it.onGround {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

// pickupItemPayload builds Pickup Item (0x67): the client plays the "item
// flies into the player" animation for collected → collector.
func pickupItemPayload(collected, collector int32, count int) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, collected)
	protocol.WriteVarInt32ToBuffer(&buf, collector)
	protocol.WriteVarInt32ToBuffer(&buf, int32(count))
	return buf.Bytes()
}
