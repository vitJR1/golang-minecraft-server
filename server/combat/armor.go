package combat

import "math"

// armor.go: damage reduction from worn armor, vanilla 1.20.1 style.
//
//	reduced = dmg × (1 − min(20, max(A/5, A − dmg/(2 + T/4))) / 25)
//	        × (1 − min(20, ΣProtection) / 25)
//
// where A is the sum of the four pieces' defence points and T their
// toughness (world.ArmorPoints), and ΣProtection the Protection levels
// across the pieces (EPF). Each worn piece then wears by max(1, dmg/4)
// durability, breaking at world.ToolDurability. Used for melee, lava/fire
// and explosions alike.

// ArmorReduce is dmg after armor of the given defence points and toughness
// and a Protection total of epf (capped at 20).
func ArmorReduce(dmg, points, toughness float32, epf int) float32 {
	armorFactor := float32(math.Max(float64(points/5), float64(points-dmg/(2+toughness/4))))
	armorFactor = float32(math.Min(20, float64(armorFactor)))
	out := dmg * (1 - armorFactor/25)
	if epf > 20 {
		epf = 20
	}
	return out * (1 - float32(epf)/25)
}

// ArmorWear is how much durability each worn piece loses to a hit of dmg.
func ArmorWear(dmg float32) int { return max(1, int(dmg/4)) }
