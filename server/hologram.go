package server

import (
	"bytes"
	"encoding/json"

	"minecraft-server/protocol"
)

// hologram.go implements floating text: an invisible, gravity-less marker
// armor stand whose custom name is always shown. Games use it for labels and
// timers hovering over a point in the world (BedWars generator countdowns).
// The name is a plain string; legacy §-colour codes render as-is on 1.20.1.

// Armor stand entity type id (protocol 763).
const armorStandEntityTypeID int32 = 2

// Entity metadata for the hologram armor stand (1.20.1 indices / types).
const (
	metaEntityFlags       = 0    // Byte: 0x20 = invisible
	metaCustomName        = 2    // OptChat
	metaCustomNameVisible = 3    // Boolean
	metaNoGravity         = 5    // Boolean
	metaArmorStandFlags   = 15   // Byte: 0x01 small, 0x10 marker (no hitbox)
	metaTypeByte          = 0    // metadata value types
	metaTypeOptChat       = 6    //
	metaTypeBoolean       = 8    //
	flagInvisible         = 0x20 //
	flagStandSmall        = 0x01 //
	flagStandMarker       = 0x10 //
)

// hologram is one floating-text entity in an instance.
type hologram struct {
	inst    *Instance
	eid     int32
	uuid    [16]byte
	x, y, z float64
	text    string
}

// SpawnHologram creates floating text at (x, y, z) — the text renders roughly
// half a block above that point — and broadcasts it to everyone in the
// instance. Returns nil when the instance has no server (entity-ID
// allocator).
func (i *Instance) SpawnHologram(x, y, z float64, text string) *hologram {
	if i.Server == nil {
		return nil
	}
	h := &hologram{
		inst: i,
		eid:  i.Server.nextEntityID.Add(1),
		x:    x,
		y:    y,
		z:    z,
		text: text,
	}
	h.uuid = entityUUID(h.eid)

	i.holoMu.Lock()
	i.holograms = append(i.holograms, h)
	spawn, meta := spawnHologramPayload(h), hologramMetadataPayload(h)
	i.holoMu.Unlock()

	i.Players.Broadcast(CbPlaySpawnEntity, spawn, -1)
	i.Players.Broadcast(CbPlaySetEntityMetadata, meta, -1)
	return h
}

// SetText replaces the floating text and pushes it to every viewer. A no-op
// when the text hasn't changed, so callers can refresh every tick cheaply.
func (h *hologram) SetText(text string) {
	i := h.inst
	i.holoMu.Lock()
	if h.text == text {
		i.holoMu.Unlock()
		return
	}
	h.text = text
	meta := hologramMetadataPayload(h)
	i.holoMu.Unlock()
	i.Players.Broadcast(CbPlaySetEntityMetadata, meta, -1)
}

// Text returns the current floating text.
func (h *hologram) Text() string {
	h.inst.holoMu.Lock()
	defer h.inst.holoMu.Unlock()
	return h.text
}

// Remove despawns the hologram for everyone. Safe to call twice.
func (h *hologram) Remove() {
	i := h.inst
	i.holoMu.Lock()
	found := false
	for idx, other := range i.holograms {
		if other == h {
			i.holograms = append(i.holograms[:idx], i.holograms[idx+1:]...)
			found = true
			break
		}
	}
	i.holoMu.Unlock()
	if found {
		i.Players.Broadcast(CbPlayRemoveEntities, removeEntitiesPayload([]int32{h.eid}), -1)
	}
}

// sendHolograms spawns every hologram in the instance to this client. Called
// from sendWorldEntities on join and after every Respawn.
func (c *ClientConnection) sendHolograms() error {
	type outPkt struct {
		id      int32
		payload []byte
	}
	c.instance.holoMu.Lock()
	pkts := make([]outPkt, 0, 2*len(c.instance.holograms))
	for _, h := range c.instance.holograms {
		pkts = append(pkts,
			outPkt{CbPlaySpawnEntity, spawnHologramPayload(h)},
			outPkt{CbPlaySetEntityMetadata, hologramMetadataPayload(h)})
	}
	c.instance.holoMu.Unlock()

	for _, p := range pkts {
		if err := c.safeWrite(p.id, p.payload); err != nil {
			return err
		}
	}
	return nil
}

// spawnHologramPayload builds Spawn Entity (0x01) for the armor stand.
func spawnHologramPayload(h *hologram) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, h.eid)
	buf.Write(h.uuid[:])
	protocol.WriteVarInt32ToBuffer(&buf, armorStandEntityTypeID)
	buf.Write(protocol.WriteDouble(h.x))
	buf.Write(protocol.WriteDouble(h.y))
	buf.Write(protocol.WriteDouble(h.z))
	buf.WriteByte(0)                        // pitch
	buf.WriteByte(0)                        // yaw
	buf.WriteByte(0)                        // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, 0) // data
	buf.Write(protocol.WriteShort(0))
	buf.Write(protocol.WriteShort(0))
	buf.Write(protocol.WriteShort(0))
	return buf.Bytes()
}

// hologramMetadataPayload builds Set Entity Metadata (0x52): invisible, no
// gravity, small marker stand, custom name shown.
func hologramMetadataPayload(h *hologram) []byte {
	name, _ := json.Marshal(map[string]string{"text": h.text})

	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, h.eid)

	buf.WriteByte(metaEntityFlags)
	protocol.WriteVarInt32ToBuffer(&buf, metaTypeByte)
	buf.WriteByte(flagInvisible)

	buf.WriteByte(metaCustomName)
	protocol.WriteVarInt32ToBuffer(&buf, metaTypeOptChat)
	buf.WriteByte(1) // present
	buf.Write(protocol.WriteString(string(name)))

	buf.WriteByte(metaCustomNameVisible)
	protocol.WriteVarInt32ToBuffer(&buf, metaTypeBoolean)
	buf.WriteByte(1)

	buf.WriteByte(metaNoGravity)
	protocol.WriteVarInt32ToBuffer(&buf, metaTypeBoolean)
	buf.WriteByte(1)

	buf.WriteByte(metaArmorStandFlags)
	protocol.WriteVarInt32ToBuffer(&buf, metaTypeByte)
	buf.WriteByte(flagStandSmall | flagStandMarker)

	buf.WriteByte(0xFF) // end of metadata
	return buf.Bytes()
}
