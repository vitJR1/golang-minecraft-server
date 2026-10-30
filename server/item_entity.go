package server

import (
	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/inventory"
	"minecraft-server/server/itementity"
	"minecraft-server/server/visibility"
	"minecraft-server/world"
)

// item_entity.go keeps an instance's dropped items (server/itementity) and
// mirrors them to the clients: spawning (with merging into a stack close by),
// the tick — physics, pickup by the first player whose pickup box holds the
// item, despawn — and the stream for joining players.

// DropItem spawns count of the namespaced item at (x, y, z) as a dropped-item
// entity, broadcasting it to everyone in the instance. A same-item drop that
// is already lying within itementity.MergeRadius absorbs the new units instead (up
// to a full stack), so a generator that fires faster than players collect
// grows one stack rather than littering entities. Returns false for an
// unknown item id or a non-positive count.
func (i *Instance) DropItem(x, y, z float64, itemName string, count int) bool {
	itemID, ok := world.ItemByName(itemName)
	if !ok {
		return false
	}
	return i.spawnStack(inventory.Stack{ID: itemID, Count: byte(min(count, inventory.MaxStack))}, x, y, z, 0, itementity.PopVelocity, 0, itementity.PickupDelay, true)
}

// ThrowItem spawns count of itemID flying from (x, y, z) with the given
// velocity — a player's Q-drop or a stack dropped off the cursor. Thrown
// items never merge into a resting stack (they're in flight) and can't be
// collected for itementity.ThrowDelay ticks, so the thrower doesn't vacuum it
// straight back up.
func (i *Instance) ThrowItem(itemID int32, count int, x, y, z, vx, vy, vz float64) bool {
	return i.ThrowStack(inventory.Stack{ID: itemID, Count: byte(min(count, inventory.MaxStack))}, x, y, z, vx, vy, vz)
}

// ThrowStack is ThrowItem for a full stack (name, enchantments, …).
func (i *Instance) ThrowStack(st inventory.Stack, x, y, z, vx, vy, vz float64) bool {
	return i.spawnStack(st, x, y, z, vx, vy, vz, itementity.ThrowDelay, false)
}

func (i *Instance) spawnStack(st inventory.Stack, x, y, z, vx, vy, vz float64, pickupAt int, merge bool) bool {
	count := int(st.Count)
	if count <= 0 || i.Server == nil {
		return false
	}
	if _, ok := world.ItemName(st.ID); !ok {
		return false
	}
	limit := inventory.MaxStackFor(st.ID)

	i.itemsMu.Lock()
	// Merge into a nearby stack of the same item first (airborne or not — the
	// distance check already bounds how far apart they can be).
	if merge {
		for _, it := range i.items {
			if it.CanMerge(st, count, limit, x, y, z) {
				it.Stack.Count += byte(count)
				it.Ticks = 0 // a topped-up stack is "fresh" again for despawn purposes
				meta := it.MetadataPayload()
				i.itemsMu.Unlock()
				i.Players.Broadcast(packetid.CbPlaySetEntityMetadata, meta, -1)
				return true
			}
		}
	}
	eid := i.Server.nextEntityID.Add(1)
	it := &itementity.Entity{
		EID: eid, UUID: entityUUID(eid), Stack: st,
		X: x, Y: y, Z: z, VX: vx, VY: vy, VZ: vz,
		PickupAt: pickupAt,
	}
	i.items = append(i.items, it)
	spawn, meta := it.SpawnPayload(), it.MetadataPayload()
	i.itemsMu.Unlock()

	i.Players.Broadcast(packetid.CbPlaySpawnEntity, spawn, -1)
	i.Players.Broadcast(packetid.CbPlaySetEntityMetadata, meta, -1)
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
		if it.Stack.ID == itemID && it.Near(x, y, z, radius) {
			total += int(it.Stack.Count)
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
		it        *itementity.Entity
		collector *ClientConnection
		taken     int
		remaining int
	}
	var (
		moved   []*itementity.Entity
		removed []int32
		pickups []pickup
		players = i.Players.snapshot()
		kept    = i.items[:0]
	)
	for _, it := range i.items {
		mv, out := it.Step(i.solidAt)
		if mv {
			moved = append(moved, it)
		}
		if out || it.Expired() {
			removed = append(removed, it.EID)
			continue
		}
		if it.Collectable() {
			if c := i.pickupCandidate(it.X, it.Y, it.Z, players); c != nil {
				remaining := c.giveStack(it.Stack)
				if taken := int(it.Stack.Count) - remaining; taken > 0 {
					pickups = append(pickups, pickup{it: it, collector: c, taken: taken, remaining: remaining})
					it.Stack.Count = byte(remaining)
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
		out = append(out, outPkt{packetid.CbPlayTeleportEntity, it.TeleportPayload()})
	}
	for _, p := range pickups {
		out = append(out, outPkt{packetid.CbPlayPickupItem, itementity.PickupPayload(p.it.EID, p.collector.player.EntityID, p.taken)})
		if p.remaining > 0 {
			out = append(out, outPkt{packetid.CbPlaySetEntityMetadata, p.it.MetadataPayload()})
		} else {
			removed = append(removed, p.it.EID)
		}
	}
	if len(removed) > 0 {
		out = append(out, outPkt{packetid.CbPlayRemoveEntities, visibility.RemoveEntitiesPayload(removed)})
	}
	i.itemsMu.Unlock()

	for _, p := range out {
		i.Players.Broadcast(p.id, p.payload, -1)
	}
}

// pickupCandidate is the first living, non-spectator player whose pickup box
// contains (x,y,z) (itementity.InPickupBox), or nil. Stuck arrows use it too
// — vanilla's Player.touch sweeps the same inflated box.
func (i *Instance) pickupCandidate(x, y, z float64, players []*ClientConnection) *ClientConnection {
	for _, c := range players {
		if c.player == nil || c.isClosed() {
			continue
		}
		s := c.player.Snapshot()
		if s.Dead || s.Gamemode == player.Spectator {
			continue
		}
		if itementity.InPickupBox(s.X, s.Y, s.Z, x, y, z) {
			return c
		}
	}
	return nil
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
			outPkt{packetid.CbPlaySpawnEntity, it.SpawnPayload()},
			outPkt{packetid.CbPlaySetEntityMetadata, it.MetadataPayload()})
	}
	c.instance.itemsMu.Unlock()

	for _, p := range pkts {
		if err := c.safeWrite(p.id, p.payload); err != nil {
			return err
		}
	}
	return nil
}
