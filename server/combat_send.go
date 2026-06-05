package server

import (
	"bytes"
	"encoding/json"
	"math"
	"minecraft-server/protocol"
)

// Sound categories (the VarInt "Sound Source" enum in Sound Effect).
const (
	soundCategoryPlayer = 7
)

// sendSetHealth writes Set Health (0x57) to this player's own client: the
// hearts bar (health), hunger (food), and saturation. We have no food
// system, so food rides at full and saturation at a small constant —
// enough to keep the client from showing a starving HUD.
func (c *ClientConnection) sendSetHealth(health float32) error {
	var buf bytes.Buffer
	buf.Write(protocol.WriteFloat(health))
	protocol.WriteVarInt32ToBuffer(&buf, 20) // food
	buf.Write(protocol.WriteFloat(5))        // saturation
	return c.safeWrite(CbPlaySetHealth, buf.Bytes())
}

// hurtAnimationPayload builds Hurt Animation (0x21): the entity flashes red
// and recoils. yaw is the direction (degrees) the damage came from, so the
// recoil tilts away from the attacker.
func hurtAnimationPayload(entityID int32, yaw float32) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, entityID)
	buf.Write(protocol.WriteFloat(yaw))
	return buf.Bytes()
}

// entityVelocityPayload builds Set Entity Velocity (0x54). vx/vy/vz are in
// blocks per tick; the wire format is shorts in units of 1/8000 block/tick,
// clamped to int16 range (≈ 3.9 blocks/tick max, well above any knockback).
func entityVelocityPayload(entityID int32, vx, vy, vz float64) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, entityID)
	buf.Write(protocol.WriteShort(velocityShort(vx)))
	buf.Write(protocol.WriteShort(velocityShort(vy)))
	buf.Write(protocol.WriteShort(velocityShort(vz)))
	return buf.Bytes()
}

// velocityShort converts blocks/tick to the protocol's 1/8000-block units,
// clamped to the int16 range so an absurd value can't wrap around.
func velocityShort(v float64) int16 {
	s := min(math.MaxInt16, max(math.MinInt16, math.Round(v*8000)))
	return int16(s)
}

// soundEffectPayload builds Sound Effect (0x62) for a named sound played at
// a world position. We use the inline-name form (Sound ID VarInt 0 + name)
// so we don't depend on the numeric sound registry, which shifts between
// versions. Position is fixed-point (block coord × 8). Seed is 0.
func soundEffectPayload(name string, category int32, x, y, z float64, volume, pitch float32) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, 0) // 0 → inline SoundEvent follows
	buf.Write(protocol.WriteString(name))
	buf.WriteByte(0) // has fixed range = false
	protocol.WriteVarInt32ToBuffer(&buf, category)
	buf.Write(protocol.WriteInt(int32(x * 8)))
	buf.Write(protocol.WriteInt(int32(y * 8)))
	buf.Write(protocol.WriteInt(int32(z * 8)))
	buf.Write(protocol.WriteFloat(volume))
	buf.Write(protocol.WriteFloat(pitch))
	buf.Write(protocol.WriteLong(0)) // seed
	return buf.Bytes()
}

// pvp18AttackSpeed is the generic.attack_speed value sent in 1.8 mode: high
// enough that the client's cooldown bar is always full, so fast clicking
// isn't visually penalized — matching the server's full-damage-per-hit model.
const pvp18AttackSpeed = 1024.0

// attackSpeedAttributePayload builds Update Attributes (0x6A) carrying a
// single generic.attack_speed property for the given entity. The client uses
// it to drive the attack-cooldown ("charge") bar, so we set it to match the
// instance's combat model: the configured speed in 1.9, effectively instant
// in 1.8.
func attackSpeedAttributePayload(entityID int32, attackSpeed float64) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, entityID)
	protocol.WriteVarInt32ToBuffer(&buf, 1) // property count
	buf.Write(protocol.WriteString("minecraft:generic.attack_speed"))
	buf.Write(protocol.WriteDouble(attackSpeed))
	protocol.WriteVarInt32ToBuffer(&buf, 0) // modifier count
	return buf.Bytes()
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
	if cfg.Version == PvP18 {
		speed = pvp18AttackSpeed
	}
	return c.safeWrite(CbPlayUpdateAttributes, attackSpeedAttributePayload(c.player.EntityID, speed))
}

// combatDeathPayload builds Combat Death (0x38), which makes the client open
// the death screen with the given message. playerEID is the dying player;
// killerEID is the attacker's entity id (-1 for none / environmental).
func combatDeathPayload(playerEID, killerEID int32, message string) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, playerEID)
	buf.Write(protocol.WriteInt(killerEID))
	encoded, _ := json.Marshal(map[string]string{"text": message})
	buf.Write(protocol.WriteString(string(encoded)))
	return buf.Bytes()
}
