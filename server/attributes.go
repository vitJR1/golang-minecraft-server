package server

import (
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/attributes"
	"minecraft-server/server/effect"
)

// attributes.go: syncs the generic.movement_speed attribute (built by
// server/attributes) — the 1.20.1 client only applies Speed / Slowness
// through it, never from the Entity Effect packet.

// sendMovementAttributes syncs the player's movement speed (Speed /
// Slowness levels, sprint state) to their client. Called whenever either
// effect is applied or ends, and after every Respawn, which resets the
// client's attributes.
func (c *ClientConnection) sendMovementAttributes() error {
	if c.player == nil {
		return nil
	}
	payload := attributes.MovementSpeedPayload(c.player.EntityID,
		c.effectLevel(effect.Speed), c.effectLevel(effect.Slowness), c.ac.Sprinting.Load())
	return c.safeWrite(packetid.CbPlayUpdateAttributes, payload)
}
