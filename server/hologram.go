package server

import (
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/hologram"
)

// hologram.go keeps an instance's floating text (server/hologram) and
// mirrors it to the clients: spawn, text changes, removal, the stream for
// joiners.

// floatingText is one hologram in an instance.
type floatingText struct {
	inst *Instance
	hologram.Entity
}

// SpawnHologram creates floating text at (x, y, z) — the text renders roughly
// half a block above that point — and broadcasts it to everyone in the
// instance. Returns nil when the instance has no server (entity-ID
// allocator).
func (i *Instance) SpawnHologram(x, y, z float64, text string) *floatingText {
	if i.Server == nil {
		return nil
	}
	eid := i.Server.nextEntityID.Add(1)
	h := &floatingText{inst: i, Entity: hologram.Entity{EID: eid, UUID: entityUUID(eid), X: x, Y: y, Z: z, Label: text}}

	i.holoMu.Lock()
	i.holograms = append(i.holograms, h)
	spawn, meta := h.SpawnPayload(), h.MetadataPayload()
	i.holoMu.Unlock()

	i.Players.Broadcast(packetid.CbPlaySpawnEntity, spawn, -1)
	i.Players.Broadcast(packetid.CbPlaySetEntityMetadata, meta, -1)
	return h
}

// SetText replaces the floating text and pushes it to every viewer. A no-op
// when the text hasn't changed, so callers can refresh every tick cheaply.
func (h *floatingText) SetText(text string) {
	i := h.inst
	i.holoMu.Lock()
	if h.Label == text {
		i.holoMu.Unlock()
		return
	}
	h.Label = text
	meta := h.MetadataPayload()
	i.holoMu.Unlock()
	i.Players.Broadcast(packetid.CbPlaySetEntityMetadata, meta, -1)
}

// Text returns the current floating text.
func (h *floatingText) Text() string {
	h.inst.holoMu.Lock()
	defer h.inst.holoMu.Unlock()
	return h.Label
}

// Remove despawns the hologram for everyone. Safe to call twice.
func (h *floatingText) Remove() {
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
		i.Players.Broadcast(packetid.CbPlayRemoveEntities, removeEntitiesPayload([]int32{h.EID}), -1)
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
			outPkt{packetid.CbPlaySpawnEntity, h.SpawnPayload()},
			outPkt{packetid.CbPlaySetEntityMetadata, h.MetadataPayload()})
	}
	c.instance.holoMu.Unlock()

	for _, p := range pkts {
		if err := c.safeWrite(p.id, p.payload); err != nil {
			return err
		}
	}
	return nil
}
