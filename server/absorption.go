package server

import (
	"math"

	"minecraft-server/protocol/packetid"
	"minecraft-server/server/effect"
)

// absorption.go: the Absorption effect's yellow hearts. The pool lives on
// player.Player (drained by ApplyDamage before health); the client renders
// it from the Player metadata field "Additional Hearts" (index 15, Float in
// protocol 763) — the vanilla client does NOT derive it from the Entity
// Effect packet, so the value has to be pushed explicitly, to the player's
// own client as well as to everyone else (it shows in their HUD only, but
// keeping all clients in step is free).

// grantAbsorption tops the pool up to the level's size (never shrinks an
// existing larger pool) and syncs it.
func (c *ClientConnection) grantAbsorption(level int) {
	if c.player == nil || level < 1 {
		return
	}
	c.player.SetAbsorption(effect.AbsorptionPool(level, c.player.Absorption()))
	c.syncAbsorption(false)
}

// syncAbsorption broadcasts the player's absorption as metadata to every
// client in the instance (its own included) when it differs from the last
// value sent, or always when force is set (after a Respawn the client's
// own entity starts from zero again).
func (c *ClientConnection) syncAbsorption(force bool) {
	if c.player == nil {
		return
	}
	cur := c.player.Absorption()
	bits := math.Float32bits(cur)
	if !force && c.sentAbsorption.Load() == bits {
		return
	}
	c.sentAbsorption.Store(bits)
	payload := effect.AbsorptionPayload(c.player.EntityID, cur)
	if c.instance != nil {
		c.instance.Players.Broadcast(packetid.CbPlaySetEntityMetadata, payload, -1)
	} else {
		_ = c.safeWrite(packetid.CbPlaySetEntityMetadata, payload)
	}
}
