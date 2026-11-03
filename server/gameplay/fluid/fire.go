package fluid

import "minecraft-server/world"

// Lava and fire on players (vanilla): standing in lava deals 4 damage every
// half second and sets the player on fire for 15 seconds; fire deals 1
// damage every second until it runs down; water puts it out.
const (
	lavaDamage        = 4
	lavaInvulnTicks   = 10
	lavaCheckInterval = 10
	fireFromLavaTicks = 300
	fireDamage        = 1
	fireDamageEvery   = 20
	fireInvulnTicks   = 10
)

// Burn is one tick of lava and fire for a player: the damage to deal (0:
// none) with its invulnerability window, then the player's fire ticks.
type Burn struct {
	Damage float32
	Invuln uint64
	// Fire is the fire ticks left after this tick (0: out). Ignite marks a
	// fire lava (re)started — the flames are shown if they weren't.
	Fire   int32
	Ignite bool
}

// BurnStep is the tick for a player whose feet and head are in the given
// blocks and who has left ticks of fire.
func BurnStep(feet, head world.Block, left int32, tick uint64) Burn {
	inLava := feet.Name == world.Lava.Name || head.Name == world.Lava.Name
	inWater := feet.Name == world.Water.Name || head.Name == world.Water.Name
	switch {
	case inLava:
		b := Burn{Fire: fireFromLavaTicks, Ignite: true}
		if tick%lavaCheckInterval == 0 {
			b.Damage, b.Invuln = lavaDamage, lavaInvulnTicks
		}
		return b
	case inWater, left <= 0:
		return Burn{}
	}
	b := Burn{Fire: left - 1}
	if left%fireDamageEvery == 0 {
		b.Damage, b.Invuln = fireDamage, fireInvulnTicks
	}
	return b
}
