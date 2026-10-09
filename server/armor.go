package server

import (
	"math"

	"minecraft-server/world"
)

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

// armorSlots are the window-0 indices of worn armor, helmet first.
var armorSlots = [4]int16{armorHelmetSlot, armorChestplateSlot, armorLeggingsSlot, armorBootsSlot}

// absorbDamage returns dmg after this player's armor, and wears the armor.
// A player with nothing on gets dmg back unchanged and nothing wears.
func (c *ClientConnection) absorbDamage(dmg float32) float32 {
	if dmg <= 0 {
		return dmg
	}
	var points, toughness float32
	epf := 0
	worn := 0
	for _, slot := range armorSlots {
		st := c.inv.get(slot)
		if st.empty() {
			continue
		}
		name, ok := world.ItemName(st.ID)
		if !ok {
			continue
		}
		p, t := world.ArmorPoints(name)
		if p == 0 {
			continue
		}
		worn++
		points += p
		toughness += t
		epf += c.enchantOf(st, EnchantProtection)
	}
	if worn == 0 {
		return dmg
	}
	armorFactor := float32(math.Max(float64(points/5), float64(points-dmg/(2+toughness/4))))
	armorFactor = float32(math.Min(20, float64(armorFactor)))
	out := dmg * (1 - armorFactor/25)
	if epf > 20 {
		epf = 20
	}
	out *= 1 - float32(epf)/25
	c.wearArmor(max(1, int(dmg/4)))
	return out
}

// wearArmor damages every worn piece by n (Unbreaking may spare a piece),
// emptying slots whose durability runs out and syncing them to the client.
func (c *ClientConnection) wearArmor(n int) {
	changed := false
	for _, slot := range armorSlots {
		st := c.inv.get(slot)
		if st.empty() {
			continue
		}
		name, ok := world.ItemName(st.ID)
		if !ok {
			continue
		}
		maxDamage := world.ToolDurability(name)
		if maxDamage == 0 {
			continue
		}
		if lvl := c.enchantOf(st, EnchantUnbreaking); lvl > 0 && unbreakingSpares(lvl) {
			continue
		}
		st.Damage += n
		if st.Damage >= maxDamage {
			st = itemStack{}
			if c.player != nil {
				s := c.player.Snapshot()
				c.instance.playSound("minecraft:entity.item.break", soundCategoryPlayer, s.X, s.Y, s.Z, 0.8, 1)
			}
		}
		c.inv.set(slot, st)
		_ = c.sendSetSlot(0, slot, st)
		changed = true
	}
	if changed {
		c.equipmentChanged()
	}
}
