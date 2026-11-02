// Package rules holds an instance's live rule switches: PvP, the death
// behaviour, weapon damage, TNT auto-priming and the explosion damage
// multiplier. Atomics, so /instance set and game logic can flip them while
// players fight. The zero Switches has everything off and explosions at ×1.
package rules

import (
	"math"
	"sync/atomic"
)

// Switches is embedded in server.Instance (its methods are the instance's).
type Switches struct {
	// pvp gates the whole combat system; instantRespawn picks the death
	// behaviour (instant heal+teleport vs death screen).
	pvp            atomic.Bool
	instantRespawn atomic.Bool
	// customRespawn: death leaves the player dead for the game to respawn
	// (game.Instance.SetCustomRespawn). weaponDamage: melee damage from the
	// held weapon + armour instead of the flat combat.Config value.
	// tntAutoPrime: a placed TNT block ignites at once.
	customRespawn atomic.Bool
	weaponDamage  atomic.Bool
	tntAutoPrime  atomic.Bool
	// explosionDamage: multiplier on explosion player damage, stored as
	// float64 bits + 1 so that 0 means "never set" (reads as 1) while an
	// explicit 0 still round-trips.
	explosionDamage atomic.Uint64
}

// PvPEnabled reports whether the combat system is active. SetPvP toggles
// it (thread-safe, may be called mid-fight).
func (s *Switches) PvPEnabled() bool    { return s.pvp.Load() }
func (s *Switches) SetPvP(enabled bool) { s.pvp.Store(enabled) }

// InstantRespawn reports the death behaviour: true = heal+teleport on the
// spot, false = vanilla death screen. SetInstantRespawn toggles it.
func (s *Switches) InstantRespawn() bool           { return s.instantRespawn.Load() }
func (s *Switches) SetInstantRespawn(enabled bool) { s.instantRespawn.Store(enabled) }

// CustomRespawn / WeaponDamage / TNTAutoPrime are the game-tunable rule
// switches (see game.Instance). All default to off.
func (s *Switches) CustomRespawn() bool           { return s.customRespawn.Load() }
func (s *Switches) SetCustomRespawn(enabled bool) { s.customRespawn.Store(enabled) }
func (s *Switches) WeaponDamage() bool            { return s.weaponDamage.Load() }
func (s *Switches) SetWeaponDamage(enabled bool)  { s.weaponDamage.Store(enabled) }
func (s *Switches) TNTAutoPrime() bool            { return s.tntAutoPrime.Load() }
func (s *Switches) SetTNTAutoPrime(enabled bool)  { s.tntAutoPrime.Store(enabled) }

// ExplosionDamage is the multiplier applied to explosion player damage
// (game.Instance.SetExplosionDamage); 1 until a game sets it.
func (s *Switches) ExplosionDamage() float64 {
	v := s.explosionDamage.Load()
	if v == 0 {
		return 1
	}
	return math.Float64frombits(v - 1)
}

// SetExplosionDamage sets the explosion damage multiplier (negative → 0).
func (s *Switches) SetExplosionDamage(scale float64) {
	s.explosionDamage.Store(math.Float64bits(max(scale, 0)) + 1)
}
