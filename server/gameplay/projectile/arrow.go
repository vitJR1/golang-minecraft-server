package projectile

import "math"

// Bow and arrow tuning (vanilla).
const (
	ArrowMinForce           = 0.1  // a draw weaker than this shoots nothing
	ArrowStuckTicks         = 1200 // 60 s in a block, then gone
	PunchStrength           = 0.6  // horizontal blocks/tick per Punch level
	arrowBaseDamage float32 = 2
	arrowCritScale  float32 = 1.5
)

// Pickup is who may collect a stuck arrow (vanilla AbstractArrow.Pickup).
type Pickup uint8

const (
	PickupDisallowed   Pickup = iota // thrown items, game projectiles
	PickupAllowed                    // a plain survival shot: anyone gets the arrow back
	PickupCreativeOnly               // creative / Infinity shot: only creative clears it, nothing is gained
)

// DrawForce is the vanilla charge curve for a bow held for ticks:
// min(1, (t² + 2t)/3) for t seconds.
func DrawForce(ticks uint64) float64 {
	t := float64(ticks) / 20
	f := (t*t + 2*t) / 3
	if f > 1 {
		f = 1
	}
	return f
}

// ArrowDamage is the hit damage of an arrow travelling at speed blocks/tick:
// ceil(speed × (2 + Power bonus)), Power bonus 0.5·lvl + 0.5, ×1.5 when crit
// (a full draw).
func ArrowDamage(speed float64, power int, crit bool) float32 {
	per := arrowBaseDamage
	if power > 0 {
		per += 0.5*float32(power) + 0.5
	}
	dmg := float32(math.Ceil(speed * float64(per)))
	if crit {
		dmg *= arrowCritScale
	}
	return dmg
}
