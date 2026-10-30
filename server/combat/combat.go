// Package combat is the PvP model's math and wire formats, independent of
// connections: the per-instance Config (1.8 or 1.9 combat), the attack
// cooldown and charge scaling, knockback direction, the vanilla armor and
// Protection formula (ArmorReduce, ArmorWear), and the payloads of Hurt
// Animation, Set Entity Velocity, the attack-speed attribute and Combat
// Death. The server applies them to players (server/combat.go, armor.go).
package combat

import (
	"bytes"
	"encoding/json"
	"math"
	"minecraft-server/cfg"
	"minecraft-server/protocol"
)

// PvPVersion selects the melee combat model for an instance. The integer
// values match the PVP_VERSION env var (1 = 1.8, 2 = 1.9).
type PvPVersion int

const (
	PvP18 PvPVersion = 1 // 1.8: no attack cooldown, full damage per hit
	PvP19 PvPVersion = 2 // 1.9: attack-cooldown charge scaling
)

// CooldownTicks converts an attack-speed attribute (attacks/second) into the
// tick window the charge bar refills over. Guards a non-positive speed so a
// misconfigured instance can't divide by zero (treated as "instant").
func CooldownTicks(attackSpeed float32, tickRate int) float64 {
	if attackSpeed <= 0 {
		return 0
	}
	return float64(tickRate) / float64(attackSpeed)
}

// AttackStrengthScale is vanilla's getAttackStrengthScale(0.5): how charged
// the attack is, in [0,1]. ticksSince is ticks elapsed since the attacker's
// previous swing; the +0.5 matches vanilla's mid-tick sampling. A zero (or
// negative) cooldown means always fully charged.
func AttackStrengthScale(ticksSince uint64, cooldown float64) float32 {
	if cooldown <= 0 {
		return 1
	}
	f := (float64(ticksSince) + 0.5) / cooldown
	return float32(max(0, min(1, f)))
}

// ScaledDamage applies the 1.9 charge curve: a fully charged hit deals the
// full base, a freshly-spammed hit deals 20%. damage = base*(0.2+0.8*s²).
func ScaledDamage(base, strength float32) float32 {
	return base * (0.2 + strength*strength*0.8)
}

// KnockbackHoriz returns the horizontal velocity (blocks/tick) to launch the
// victim directly away from the attacker, at the given strength. When the
// two share a column (degenerate direction) it pushes along +Z so the hit
// still registers visibly.
func KnockbackHoriz(attackerX, attackerZ, victimX, victimZ float64, strength float32) (vx, vz float64) {
	dx := victimX - attackerX
	dz := victimZ - attackerZ
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		return 0, float64(strength)
	}
	return dx / dist * float64(strength), dz / dist * float64(strength)
}

// YawTo computes the Minecraft yaw (degrees) pointing from (fromX,fromZ)
// toward (toX,toZ). Used for the Hurt Animation recoil direction.
func YawTo(fromX, fromZ, toX, toZ float64) float32 {
	dx := toX - fromX
	dz := toZ - fromZ
	return float32(math.Atan2(-dx, dz) * 180 / math.Pi)
}

// Config tunes the 1.9-style PvP system per instance. Zero value is
// not usable — construct via DefaultConfig and tweak. All damage is
// in half-heart units (20 = full health). These are the numeric knobs only;
// the live on/off toggles (PvP enabled, instant respawn) live on Instance as
// atomics so they can be flipped at runtime (e.g. via /instance set pvp).
type Config struct {
	// Version is the combat model: PvP18 (no cooldown) or PvP19 (charge
	// scaling). Defaults from cfg.PvPVersion (PVP_VERSION env var).
	Version PvPVersion

	// BaseDamage is the weapon's raw hit damage at full charge, before the
	// cooldown scaling and crit multiplier. 6 ≈ a vanilla iron sword.
	BaseDamage float32

	// AttackSpeed is attacks-per-second (vanilla "Attack Speed" attribute):
	// fist 4.0, sword 1.6, axe 1.0. Drives the cooldown charge window —
	// cooldown is 1/AttackSpeed seconds.
	AttackSpeed float32

	// Knockback is the horizontal launch strength of a normal hit, in
	// blocks/tick. SprintKnockback is the extra added when the attacker is
	// sprinting (the 1.9 "w-tap"). VerticalKnockback is the upward launch.
	Knockback         float32
	SprintKnockback   float32
	VerticalKnockback float32

	// CritMultiplier scales damage on a critical hit (attacker falling, at
	// full charge). Vanilla is 1.5.
	CritMultiplier float32

	// InvulnTicks is the per-target i-frame window (vanilla: 10 ticks).
	InvulnTicks uint64

	// RegenIntervalTicks is how often a below-max, alive player heals one
	// half-heart. 0 disables natural regen.
	RegenIntervalTicks uint64
}

// DefaultConfig returns the out-of-the-box PvP tuning: a sword-like
// weapon with the full 1.9 cooldown/crit/knockback model and slow natural
// regen. Enabled by default — disable it on safe instances (e.g. the hub).
func DefaultConfig() Config {
	return Config{
		Version:            VersionFromCfg(),
		BaseDamage:         6,
		AttackSpeed:        1.6,
		Knockback:          0.4,
		SprintKnockback:    0.4,
		VerticalKnockback:  0.4,
		CritMultiplier:     1.5,
		InvulnTicks:        10,
		RegenIntervalTicks: 40, // ~2s per half-heart
	}
}

// VersionFromCfg maps the cfg.PvPVersion int (set from PVP_VERSION) to a
// PvPVersion, falling back to 1.9 for any unrecognized value.
func VersionFromCfg() PvPVersion {
	if cfg.PvPVersion == int(PvP18) {
		return PvP18
	}
	return PvP19
}

// HurtAnimationPayload builds Hurt Animation (0x21): the entity flashes red
// and recoils. yaw is the direction (degrees) the damage came from, so the
// recoil tilts away from the attacker.
func HurtAnimationPayload(entityID int32, yaw float32) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, entityID)
	buf.Write(protocol.WriteFloat(yaw))
	return buf.Bytes()
}

// EntityVelocityPayload builds Set Entity Velocity (0x54). vx/vy/vz are in
// blocks per tick; the wire format is shorts in units of 1/8000 block/tick,
// clamped to int16 range (≈ 3.9 blocks/tick max, well above any knockback).
func EntityVelocityPayload(entityID int32, vx, vy, vz float64) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, entityID)
	buf.Write(protocol.WriteShort(protocol.VelocityShort(vx)))
	buf.Write(protocol.WriteShort(protocol.VelocityShort(vy)))
	buf.Write(protocol.WriteShort(protocol.VelocityShort(vz)))
	return buf.Bytes()
}

// PvP18AttackSpeed is the generic.attack_speed value sent in 1.8 mode: high
// enough that the client's cooldown bar is always full, so fast clicking
// isn't visually penalized — matching the server's full-damage-per-hit model.
const PvP18AttackSpeed = 1024.0

// AttackSpeedAttributePayload builds Update Attributes (0x6A) carrying a
// single generic.attack_speed property for the given entity. The client uses
// it to drive the attack-cooldown ("charge") bar, so we set it to match the
// instance's combat model: the configured speed in 1.9, effectively instant
// in 1.8.
func AttackSpeedAttributePayload(entityID int32, attackSpeed float64) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, entityID)
	protocol.WriteVarInt32ToBuffer(&buf, 1) // property count
	buf.Write(protocol.WriteString("minecraft:generic.attack_speed"))
	buf.Write(protocol.WriteDouble(attackSpeed))
	protocol.WriteVarInt32ToBuffer(&buf, 0) // modifier count
	return buf.Bytes()
}

// DeathPayload builds Combat Death (0x38), which makes the client open
// the death screen with the given message. playerEID is the dying player;
// killerEID is the attacker's entity id (-1 for none / environmental).
func DeathPayload(playerEID, killerEID int32, message string) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, playerEID)
	buf.Write(protocol.WriteInt(killerEID))
	encoded, _ := json.Marshal(map[string]string{"text": message})
	buf.Write(protocol.WriteString(string(encoded)))
	return buf.Bytes()
}
