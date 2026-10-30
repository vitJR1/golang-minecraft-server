package server

import (
	"bytes"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/combat"
	"minecraft-server/server/sound"
)

// playSound broadcasts a positional sound to the players who can hear it.
func (i *Instance) playSound(name string, category int32, x, y, z float64, volume, pitch float32) {
	rng := sound.BaseRange
	if volume > 1 {
		rng *= float64(volume)
	}
	i.Players.BroadcastNear(x, y, z, rng, packetid.CbPlaySoundEffect,
		sound.EffectPayload(name, category, x, y, z, volume, pitch), -1)
}

// sendSetHealth writes Set Health (0x57) to this player's own client: the
// hearts bar (health), hunger (food), and saturation. We have no food
// system, so food rides at full and saturation at a small constant —
// enough to keep the client from showing a starving HUD. Every health
// sync also re-syncs the absorption pool when it changed (damage drains
// absorption before health, see player.ApplyDamage), so the yellow hearts
// follow the red ones without each damage source having to remember.
func (c *ClientConnection) sendSetHealth(health float32) error {
	var buf bytes.Buffer
	buf.Write(protocol.WriteFloat(health))
	protocol.WriteVarInt32ToBuffer(&buf, 20) // food
	buf.Write(protocol.WriteFloat(5))        // saturation
	err := c.safeWrite(packetid.CbPlaySetHealth, buf.Bytes())
	c.syncAbsorption(false)
	return err
}

// sendCombatAttributes tells this player's client which attack-cooldown bar
// to render for the instance's combat model. Sent on join, cross-instance
// move, and respawn — the client resets entity attributes whenever it gets a
// Respawn packet, so each of those paths must re-send.
func (c *ClientConnection) sendCombatAttributes() error {
	if c.player == nil {
		return nil
	}
	cfg := c.instance.Combat
	speed := float64(cfg.AttackSpeed)
	if cfg.Version == combat.PvP18 {
		speed = combat.PvP18AttackSpeed
	}
	return c.safeWrite(packetid.CbPlayUpdateAttributes, combat.AttackSpeedAttributePayload(c.player.EntityID, speed))
}
