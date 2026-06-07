package server

import (
	"bytes"
	"encoding/binary"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// world_entities.go spawns the non-player entities baked into an instance's
// world — item frames today. Each is assigned a stable server entity ID at
// instance construction (loadWorldEntities) so every viewer shares it, then
// sent to each client on join / move / respawn via sendWorldEntities.

// hotbarSlotBase is the player-inventory slot index of hotbar slot 0; hotbar
// slots 0..8 map to inventory slots 36..44.
const hotbarSlotBase = 36

// onSetCreativeSlot records the item a creative player put in a slot, so
// UseItemOnBlock can place what they're holding. Reads Short slot + Slot(item);
// any trailing item NBT is left unread (the per-packet buffer is discarded).
func (c *ClientConnection) onSetCreativeSlot(packet *bytes.Buffer) {
	raw, err := protocol.ReadUShortFromBuf(packet)
	if err != nil {
		return
	}
	slot := int16(raw)

	present, err := protocol.ReadBool(packet)
	if err != nil {
		return
	}
	if c.heldItems == nil {
		c.heldItems = make(map[int16]string)
	}
	if !present {
		delete(c.heldItems, slot)
		return
	}
	itemID, err := protocol.ReadVarInt(packet)
	if err != nil {
		return
	}
	_, _ = packet.ReadByte() // count (NBT, if any, follows but we don't need it)
	if name, ok := world.ItemName(int32(itemID)); ok {
		c.heldItems[slot] = name
	} else {
		delete(c.heldItems, slot)
	}
}

// heldItemName returns the namespaced id of the item in the player's selected
// hotbar slot, or "" if unknown (no creative-slot info yet).
func (c *ClientConnection) heldItemName() string {
	return c.heldItems[int16(hotbarSlotBase+c.heldSlot.Load())]
}

// instanceEntity is a world entity with its assigned runtime identity.
type instanceEntity struct {
	eid  int32
	uuid [16]byte
	e    world.Entity
}

// loadWorldEntities pulls the instance world's entities (if it carries any),
// assigns each a server entity ID + UUID, and records them. No-op when the
// world has no entities or the server (entity-ID allocator) is absent.
func (i *Instance) loadWorldEntities() {
	if i.Server == nil || i.World == nil {
		return
	}
	ep, ok := i.World.(world.EntityProvider)
	if !ok {
		return
	}
	i.entitiesMu.Lock()
	for _, e := range ep.Entities() {
		if _, ok := world.EntityTypeID(e.Type); !ok {
			continue // unknown entity kind — can't spawn it
		}
		eid := i.Server.nextEntityID.Add(1)
		i.worldEntities = append(i.worldEntities, instanceEntity{
			eid:  eid,
			uuid: entityUUID(eid),
			e:    e,
		})
	}
	i.entitiesMu.Unlock()
}

// AddWorldEntity assigns a runtime entity ID to e, records it, and broadcasts
// its spawn to everyone currently in the instance. New joiners pick it up via
// sendWorldEntities. Used for player-placed item frames.
func (i *Instance) AddWorldEntity(e world.Entity) {
	if i.Server == nil {
		return
	}
	eid := i.Server.nextEntityID.Add(1)
	ie := instanceEntity{eid: eid, uuid: entityUUID(eid), e: e}

	i.entitiesMu.Lock()
	i.worldEntities = append(i.worldEntities, ie)
	spawn := spawnEntityPayload(ie)
	meta := frameMetadataPayload(ie)
	i.entitiesMu.Unlock()

	i.Players.Broadcast(CbPlaySpawnEntity, spawn, -1)
	if meta != nil {
		i.Players.Broadcast(CbPlaySetEntityMetadata, meta, -1)
	}
}

// entityUUID derives a deterministic UUID from an entity ID. The client only
// needs uniqueness; encoding the eid in the low bytes guarantees it.
func entityUUID(eid int32) [16]byte {
	var u [16]byte
	u[6] = 0x40 // version 4 nibble (cosmetic; client doesn't validate)
	u[8] = 0x80 // variant bits
	binary.BigEndian.PutUint32(u[12:], uint32(eid))
	return u
}

// sendWorldEntities spawns every baked world entity to this client. Called on
// join and after any Respawn (which wipes client-side entities). A no-op for
// instances without entities, so the hub and plain arenas cost nothing.
func (c *ClientConnection) sendWorldEntities() error {
	// Build all payloads under the lock — frameMetadataPayload reads mutable
	// FrameData (item/rotation), which FrameInteract can change concurrently.
	type outPkt struct {
		id      int32
		payload []byte
	}
	c.instance.entitiesMu.Lock()
	pkts := make([]outPkt, 0, len(c.instance.worldEntities))
	for _, ie := range c.instance.worldEntities {
		pkts = append(pkts, outPkt{CbPlaySpawnEntity, spawnEntityPayload(ie)})
		if meta := frameMetadataPayload(ie); meta != nil {
			pkts = append(pkts, outPkt{CbPlaySetEntityMetadata, meta})
		}
	}
	c.instance.entitiesMu.Unlock()

	for _, p := range pkts {
		if err := c.safeWrite(p.id, p.payload); err != nil {
			return err
		}
	}
	return nil
}

// FrameInteract handles a right-click on an item-frame entity: it puts the
// player's held item into an empty frame, or rotates the item in a full one
// (vanilla behaviour), then broadcasts the updated metadata. No-op when eid
// isn't a frame in this instance. Frame state is mutated under entitiesMu, and
// the broadcast payload is built there too, so it can't race the chunk-join
// path that also reads frame state.
func (i *Instance) FrameInteract(eid int32, heldItem string) {
	i.entitiesMu.Lock()
	var meta []byte
	for idx := range i.worldEntities {
		ie := &i.worldEntities[idx]
		if ie.eid != eid || ie.e.Frame == nil {
			continue
		}
		f := ie.e.Frame
		switch {
		case f.Item == "":
			// Insert the held item, but only if it's a real item id.
			if heldItem == "" {
				i.entitiesMu.Unlock()
				return
			}
			if _, ok := world.ItemByName(heldItem); !ok {
				i.entitiesMu.Unlock()
				return
			}
			f.Item = heldItem
			f.Rotation = 0
		default:
			// Full frame → rotate the item one of 8 steps.
			f.Rotation = (f.Rotation + 1) % 8
		}
		meta = frameMetadataPayload(*ie)
		break
	}
	i.entitiesMu.Unlock()

	if meta != nil {
		i.Players.Broadcast(CbPlaySetEntityMetadata, meta, -1)
	}
}

// spawnEntityPayload builds Spawn Entity (0x01) for a world entity (item frame
// or villager). For item frames the facing rides the "data" field (vanilla
// object data) and yaw/pitch are 0; other entities use their stored yaw/pitch.
func spawnEntityPayload(ie instanceEntity) []byte {
	typeID, _ := world.EntityTypeID(ie.e.Type) // 0 (unknown) is filtered at load
	var data int32
	if f := ie.e.Frame; f != nil {
		if f.Glowing {
			typeID = world.GlowItemFrameEntityID
		}
		data = int32(f.Facing)
	}

	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, ie.eid)
	buf.Write(ie.uuid[:])
	protocol.WriteVarInt32ToBuffer(&buf, typeID)
	buf.Write(protocol.WriteDouble(ie.e.X))
	buf.Write(protocol.WriteDouble(ie.e.Y))
	buf.Write(protocol.WriteDouble(ie.e.Z))
	buf.WriteByte(protocol.AngleToByte(ie.e.Pitch))
	buf.WriteByte(protocol.AngleToByte(ie.e.Yaw))
	buf.WriteByte(protocol.AngleToByte(ie.e.Yaw)) // head yaw = body yaw
	protocol.WriteVarInt32ToBuffer(&buf, data)
	buf.Write(protocol.WriteShort(0)) // velocity x
	buf.Write(protocol.WriteShort(0)) // velocity y
	buf.Write(protocol.WriteShort(0)) // velocity z
	return buf.Bytes()
}

// frameMetadataPayload builds Set Entity Metadata (0x52) for an item frame:
// the displayed item (index 8, Slot) and its rotation (index 9, VarInt).
// Returns nil when there's nothing to say (empty frame, no rotation) so the
// caller can skip the packet.
func frameMetadataPayload(ie instanceEntity) []byte {
	f := ie.e.Frame
	if f == nil {
		return nil
	}
	itemID, haveItem := int32(0), false
	if f.Item != "" {
		if id, ok := world.ItemByName(f.Item); ok {
			itemID, haveItem = id, true
		}
	}
	if !haveItem && f.Rotation == 0 {
		return nil // nothing non-default to send
	}

	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, ie.eid)
	if haveItem {
		// Index 8: Item (metadata type 7 = Slot).
		buf.WriteByte(8)
		protocol.WriteVarInt32ToBuffer(&buf, 7)
		buf.WriteByte(1) // slot present
		protocol.WriteVarInt32ToBuffer(&buf, itemID)
		buf.WriteByte(1)    // count
		buf.WriteByte(0x00) // no item NBT (TAG_End)
	}
	if f.Rotation != 0 {
		// Index 9: Rotation (metadata type 1 = VarInt).
		buf.WriteByte(9)
		protocol.WriteVarInt32ToBuffer(&buf, 1)
		protocol.WriteVarInt32ToBuffer(&buf, int32(f.Rotation))
	}
	buf.WriteByte(0xFF) // end of metadata
	return buf.Bytes()
}
