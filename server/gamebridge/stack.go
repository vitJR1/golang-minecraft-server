package gamebridge

import (
	"minecraft-server/game"
	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

// ToGame converts a model stack for plugins. slot is the window-0 index
// (or -1 when unknown). Enchantments and Lore are copied.
func ToGame(st inventory.Stack, slot int) game.ItemStack {
	if st.Empty() {
		return game.ItemStack{Slot: slot}
	}
	name, _ := world.ItemName(st.ID)
	out := game.ItemStack{
		Item: name, Count: int(st.Count), Name: st.Name, Color: st.Color,
		Potion: st.Potion, Damage: st.Damage, Slot: slot, NoDrop: st.NoDrop,
	}
	if len(st.Lore) > 0 {
		out.Lore = append([]string(nil), st.Lore...)
	}
	if len(st.Enchantments) > 0 {
		out.Enchantments = make(map[string]int, len(st.Enchantments))
		for k, v := range st.Enchantments {
			out.Enchantments[k] = v
		}
	}
	return out
}

// FromGame converts a plugin stack to the model. ok is false for an
// unknown item id.
func FromGame(gs game.ItemStack) (inventory.Stack, bool) {
	if gs.Empty() {
		return inventory.Stack{}, true
	}
	id, ok := world.ItemByName(gs.Item)
	if !ok {
		return inventory.Stack{}, false
	}
	count := gs.Count
	if count > 64 {
		count = 64
	}
	st := inventory.Stack{ID: id, Count: byte(count), Name: gs.Name, Color: gs.Color, Potion: gs.Potion, Damage: gs.Damage, NoDrop: gs.NoDrop}
	if len(gs.Lore) > 0 {
		st.Lore = append([]string(nil), gs.Lore...)
	}
	if len(gs.Enchantments) > 0 {
		st.Enchantments = make(map[string]int, len(gs.Enchantments))
		for k, v := range gs.Enchantments {
			if v > 0 {
				st.Enchantments[k] = v
			}
		}
	}
	return st, true
}
