package bedwars

import (
	"strings"
	"time"

	"minecraft-server/game"
)

// kit.go is what a player owns across deaths (Hypixel rules): the armour
// tier, the pickaxe / axe tiers (each drops one tier on death, never below
// the first once bought) and permanent shears. equip() turns a kit plus the
// team's upgrades into an inventory: wooden sword, team-coloured leather
// helmet and chestplate, leggings and boots of the armour tier, tools, and
// Maniac Miner haste.

// Armour tiers (kit.armor). Leather is the default everyone has.
const (
	armorLeather = iota
	armorChain
	armorIron
	armorDiamond
)

// kit is one player's persistent loadout.
type kit struct {
	armor   int // armorLeather..armorDiamond
	pickaxe int // 0 = none, 1..maxToolTier(toolPickaxe)
	axe     int
	shears  bool
}

// onDeath applies the death penalty: tools lose a tier (but a bought tool
// never disappears). Armour and shears are permanent.
func (k *kit) onDeath() {
	if k.pickaxe > 1 {
		k.pickaxe--
	}
	if k.axe > 1 {
		k.axe--
	}
}

// toolTierOf returns the kit tier for a tool kind.
func (k *kit) toolTierOf(t toolKind) int {
	switch t {
	case toolPickaxe:
		return k.pickaxe
	case toolAxe:
		return k.axe
	}
	return 0
}

func (k *kit) setToolTier(t toolKind, tier int) {
	switch t {
	case toolPickaxe:
		k.pickaxe = tier
	case toolAxe:
		k.axe = tier
	}
}

// armorPieces maps a tier to its leggings + boots items.
var armorPieces = map[int][2]string{
	armorLeather: {"minecraft:leather_leggings", "minecraft:leather_boots"},
	armorChain:   {"minecraft:chainmail_leggings", "minecraft:chainmail_boots"},
	armorIron:    {"minecraft:iron_leggings", "minecraft:iron_boots"},
	armorDiamond: {"minecraft:diamond_leggings", "minecraft:diamond_boots"},
}

// hasteDuration is "permanent" for a round's purposes (Maniac Miner).
const hasteDuration = 24 * time.Hour

// isSword reports whether an item id is a sword.
func isSword(item string) bool { return strings.HasSuffix(item, "_sword") }

// isArmor reports whether an item id is an armour piece.
func isArmor(item string) bool {
	return strings.HasSuffix(item, "_helmet") || strings.HasSuffix(item, "_chestplate") ||
		strings.HasSuffix(item, "_leggings") || strings.HasSuffix(item, "_boots")
}

// withTeamUpgrades applies Sharpened Swords / Reinforced Armor to a stack
// the team is about to receive (swords and armour only).
func (ts *teamState) withTeamUpgrades(st game.ItemStack) game.ItemStack {
	add := func(id string, lvl int) {
		if lvl <= 0 {
			return
		}
		if st.Enchantments == nil {
			st.Enchantments = map[string]int{}
		} else {
			cp := make(map[string]int, len(st.Enchantments)+1)
			for k, v := range st.Enchantments {
				cp[k] = v
			}
			st.Enchantments = cp
		}
		if st.Enchantments[id] < lvl {
			st.Enchantments[id] = lvl
		}
	}
	switch {
	case isSword(st.Item) && ts.sharpened:
		add("minecraft:sharpness", 1)
	case isArmor(st.Item):
		add("minecraft:protection", ts.protection)
	}
	return st
}

// armorPiece builds one armour item: leather gets the team dye.
func (ts *teamState) armorPiece(item string) game.ItemStack {
	st := game.ItemStack{Item: item, Count: 1}
	if strings.HasPrefix(item, "minecraft:leather_") {
		st.Color = ts.team.Color
	}
	return ts.withTeamUpgrades(st)
}

// equip replaces p's inventory with their kit for team ts.
func (g *bedWars) equip(p game.PlayerHandle, ts *teamState, k *kit) {
	p.ClearInventory()

	p.SetSlot(game.SlotHotbar0, ts.withTeamUpgrades(game.ItemStack{Item: "minecraft:wooden_sword", Count: 1}))

	p.SetSlot(game.SlotHelmet, ts.armorPiece("minecraft:leather_helmet"))
	p.SetSlot(game.SlotChestplate, ts.armorPiece("minecraft:leather_chestplate"))
	pieces := armorPieces[k.armor]
	p.SetSlot(game.SlotLeggings, ts.armorPiece(pieces[0]))
	p.SetSlot(game.SlotBoots, ts.armorPiece(pieces[1]))

	if st, ok := toolStack(toolPickaxe, k.pickaxe); ok {
		p.GiveStack(st)
	}
	if st, ok := toolStack(toolAxe, k.axe); ok {
		p.GiveStack(st)
	}
	if k.shears {
		p.GiveStack(game.ItemStack{Item: "minecraft:shears", Count: 1})
	}
	if ts.haste > 0 {
		p.ApplyEffect("haste", ts.haste, hasteDuration)
	}
}

// kitOf returns (creating on first use) the player's kit. Caller holds g.mu.
func (g *bedWars) kitOfLocked(eid int32) *kit {
	k := g.kits[eid]
	if k == nil {
		k = &kit{}
		g.kits[eid] = k
	}
	return k
}

// lootResources counts the four currencies in p's inventory.
func lootResources(p game.PlayerHandle) map[Resource]int {
	out := map[Resource]int{}
	for _, r := range []Resource{Iron, Gold, Diamond, Emerald} {
		if n := p.CountItem(r.item()); n > 0 {
			out[r] = n
		}
	}
	return out
}
