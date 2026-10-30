package server

import (
	"minecraft-server/server/combat"
	"minecraft-server/server/inventory"
	"minecraft-server/server/sound"
	"minecraft-server/world"
)

// armor.go: worn armor against damage — the formula is combat.ArmorReduce
// (combat), the wear is applied here.

// armorSlots are the window-0 indices of worn armor, helmet first.
var armorSlots = [4]int16{inventory.HelmetSlot, inventory.ChestplateSlot, inventory.LeggingsSlot, inventory.BootsSlot}

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
		st := c.inv.Get(slot)
		if st.Empty() {
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
	out := combat.ArmorReduce(dmg, points, toughness, epf)
	c.wearArmor(combat.ArmorWear(dmg))
	return out
}

// wearArmor damages every worn piece by n (Unbreaking may spare a piece),
// emptying slots whose durability runs out and syncing them to the client.
func (c *ClientConnection) wearArmor(n int) {
	changed := false
	for _, slot := range armorSlots {
		st := c.inv.Get(slot)
		if st.Empty() {
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
			st = inventory.Stack{}
			if c.player != nil {
				s := c.player.Snapshot()
				c.instance.playSound("minecraft:entity.item.break", sound.CategoryPlayer, s.X, s.Y, s.Z, 0.8, 1)
			}
		}
		c.inv.Set(slot, st)
		_ = c.sendSetSlot(0, slot, st)
		changed = true
	}
	if changed {
		c.equipmentChanged()
	}
}
