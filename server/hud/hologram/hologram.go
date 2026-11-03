// Package hologram is floating text as entities: an invisible, gravity-less
// marker armor stand whose custom name is always shown. The server keeps
// them per instance (Instance.SpawnHologram) and streams them to joiners.
package hologram

import (
	"bytes"

	"minecraft-server/protocol"
)

// Armor stand entity type id (protocol 763).
const armorStandEntityTypeID int32 = 2

// Armor stand metadata (1.20.1); the shared indices are protocol.Meta*.
const (
	metaArmorStandFlags = 15   // Byte: 0x01 small, 0x10 marker (no hitbox)
	flagStandSmall      = 0x01 //
	flagStandMarker     = 0x10 //
)

// Entity is one hologram: its identity, position and text (§ codes work).
type Entity struct {
	EID     int32
	UUID    [16]byte
	X, Y, Z float64
	Label   string
}

// SpawnPayload builds Spawn Entity (0x01) for the armor stand.
func (h *Entity) SpawnPayload() []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, h.EID)
	buf.Write(h.UUID[:])
	protocol.WriteVarInt32ToBuffer(&buf, armorStandEntityTypeID)
	buf.Write(protocol.WriteDouble(h.X))
	buf.Write(protocol.WriteDouble(h.Y))
	buf.Write(protocol.WriteDouble(h.Z))
	buf.WriteByte(0)                        // pitch
	buf.WriteByte(0)                        // yaw
	buf.WriteByte(0)                        // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, 0) // data
	buf.Write(protocol.WriteShort(0))
	buf.Write(protocol.WriteShort(0))
	buf.Write(protocol.WriteShort(0))
	return buf.Bytes()
}

// MetadataPayload builds Set Entity Metadata (0x52): invisible, no
// gravity, small marker stand, custom name shown.
func (h *Entity) MetadataPayload() []byte {

	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, h.EID)

	buf.WriteByte(protocol.MetaEntityFlags)
	protocol.WriteVarInt32ToBuffer(&buf, protocol.MetaTypeByte)
	buf.WriteByte(protocol.EntityFlagInvisible)

	buf.WriteByte(protocol.MetaCustomName)
	protocol.WriteVarInt32ToBuffer(&buf, protocol.MetaTypeOptChat)
	buf.WriteByte(1) // present
	buf.Write(protocol.TextComponent(h.Label))

	buf.WriteByte(protocol.MetaCustomNameVisible)
	protocol.WriteVarInt32ToBuffer(&buf, protocol.MetaTypeBoolean)
	buf.WriteByte(1)

	buf.WriteByte(protocol.MetaNoGravity)
	protocol.WriteVarInt32ToBuffer(&buf, protocol.MetaTypeBoolean)
	buf.WriteByte(1)

	buf.WriteByte(metaArmorStandFlags)
	protocol.WriteVarInt32ToBuffer(&buf, protocol.MetaTypeByte)
	buf.WriteByte(flagStandSmall | flagStandMarker)

	buf.WriteByte(0xFF) // end of metadata
	return buf.Bytes()
}
