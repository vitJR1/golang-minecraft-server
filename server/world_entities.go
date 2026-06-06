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
	i.entitiesMu.Unlock()

	i.Players.Broadcast(CbPlaySpawnEntity, spawnEntityPayload(ie), -1)
	if meta := frameMetadataPayload(ie); meta != nil {
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
	c.instance.entitiesMu.Lock()
	frames := make([]instanceEntity, len(c.instance.worldEntities))
	copy(frames, c.instance.worldEntities)
	c.instance.entitiesMu.Unlock()
	for _, ie := range frames {
		if err := c.safeWrite(CbPlaySpawnEntity, spawnEntityPayload(ie)); err != nil {
			return err
		}
		if meta := frameMetadataPayload(ie); meta != nil {
			if err := c.safeWrite(CbPlaySetEntityMetadata, meta); err != nil {
				return err
			}
		}
	}
	return nil
}

// spawnEntityPayload builds Spawn Entity (0x01) for an item frame. The frame's
// facing rides the "data" field (vanilla object data), not yaw/pitch.
func spawnEntityPayload(ie instanceEntity) []byte {
	typeID := world.ItemFrameEntityID
	var facing byte
	if f := ie.e.Frame; f != nil {
		if f.Glowing {
			typeID = world.GlowItemFrameEntityID
		}
		facing = f.Facing
	}

	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, ie.eid)
	buf.Write(ie.uuid[:])
	protocol.WriteVarInt32ToBuffer(&buf, typeID)
	buf.Write(protocol.WriteDouble(ie.e.X))
	buf.Write(protocol.WriteDouble(ie.e.Y))
	buf.Write(protocol.WriteDouble(ie.e.Z))
	buf.WriteByte(0) // pitch (frame orientation comes from data)
	buf.WriteByte(0) // yaw
	buf.WriteByte(0) // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, int32(facing))
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
