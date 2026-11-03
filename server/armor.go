package server

import (
	"minecraft-server/server/gameplay/combat"
	"minecraft-server/server/gameplay/enchant"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/server/gameplay/sound"
	"minecraft-server/world"
)

// armor.go: worn armor against damage — the formula is combat.ArmorReduce
// (combat), the wear is applied here.

// armorSlots are the window-0 indices of worn armor, helmet first.
var armorSlots = [4]int16{inventory.HelmetSlot, inventory.ChestplateSlot, inventory.LeggingsSlot, inventory.BootsSlot}

// absorbDamage returns dmg after this player's armor and the wear the hit
// would do to it. The caller wears the armor (wearArmor) only once the hit
// is applied — one swallowed by the invulnerability frames wears nothing,
// like vanilla. A player with nothing on gets dmg back and no wear.
func (c *ClientConnection) absorbDamage(dmg float32) (out float32, wear int) {
	if dmg <= 0 {
		return dmg, 0
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
		epf += c.enchantOf(st, enchant.Protection)
	}
	if worn == 0 {
		return dmg, 0
	}
	return combat.ArmorReduce(dmg, points, toughness, epf), combat.ArmorWear(dmg)
}

// wearArmor damages every worn piece by n (Unbreaking may spare a piece),
// emptying slots whose durability runs out and syncing them to the client.
func (c *ClientConnection) wearArmor(n int) {
	changed := false
	for _, slot := range armorSlots {
		st, worn, broke := c.wearSlot(slot, n)
		if !worn {
			continue
		}
		if broke && c.player != nil {
			s := c.player.Snapshot()
			c.instance.playSound("minecraft:entity.item.break", sound.CategoryPlayer, s.X, s.Y, s.Z, 0.8, 1)
		}
		_ = c.sendSetSlot(0, slot, st)
		changed = true
	}
	if changed {
		c.equipmentChanged()
	}
}
