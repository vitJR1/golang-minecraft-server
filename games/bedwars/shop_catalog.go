package bedwars

import (
	"fmt"
	"strings"

	"minecraft-server/game"
)

// shop_catalog.go is the item shop's data: seven Hypixel-style categories,
// each a list of shopItems with a price in one of the four resources. The
// UI (shop.go) renders it and hands purchases to buyItem; kit.go turns the
// tiered entries (armour, tools) into inventory.

// itemKind decides how a purchase is applied.
type itemKind int

const (
	kindPlain     itemKind = iota // give the stack
	kindArmor                     // permanent armour tier (kit.armor)
	kindTool                      // tiered pickaxe / axe (kit.pickaxe / kit.axe)
	kindPermanent                 // one-time permanent item (shears)
)

// toolKind selects which kit tier a kindTool entry advances.
type toolKind int

const (
	toolNone toolKind = iota
	toolPickaxe
	toolAxe
)

// shopItem is one purchasable entry. For kindTool the displayed item and
// price depend on the buyer's current tier (see toolTiers); for everything
// else Icon/Count/Cost/Price are fixed.
type shopItem struct {
	Key   string   // stable id, e.g. "wool", "stone_sword", "pickaxe"
	Label string   // "§aWool"
	Icon  string   // item id shown in the menu (team placeholders allowed)
	Count int      // units per purchase (and icon stack size)
	Cost  Resource // currency
	Price int      // units of Cost
	Desc  []string // lore description lines (§7 prefixed by the renderer)
	Kind  itemKind
	Tier  int      // kindArmor: 1 chainmail, 2 iron, 3 diamond
	Tool  toolKind // kindTool
	// Stack builds the goods for kindPlain / kindPermanent, coloured for the
	// buyer's team. nil → a plain stack of Icon×Count.
	Stack func(t Team) game.ItemStack
}

type shopCategory struct {
	Name  string
	Icon  string
	Items []shopItem
}

// Resource → item id / display name helpers for prices.
func (r Resource) item() string { return resourceItem[r] }

func (r Resource) colour() string {
	switch r {
	case Gold:
		return "§6"
	case Diamond:
		return "§b"
	case Emerald:
		return "§2"
	default:
		return "§f"
	}
}

// Team-colour placeholders resolved at purchase time.
const (
	goodsWool       = "wool"
	goodsTerracotta = "terracotta"
	goodsGlass      = "glass"
)

// resolveGoods maps a team-colour placeholder to the team's item: the wool
// block's name doubles as the item id, and terracotta / stained glass share
// the colour prefix ("minecraft:red_wool" → "minecraft:red_terracotta").
func resolveGoods(goods string, t Team) string {
	colour := strings.TrimSuffix(t.Wool.Name, "_wool")
	switch goods {
	case goodsWool:
		return t.Wool.Name
	case goodsTerracotta:
		return colour + "_terracotta"
	case goodsGlass:
		return colour + "_stained_glass"
	default:
		return goods
	}
}

// plain is a shopItem whose goods are Icon×Count with team colour applied.
func plain(key, label, icon string, count int, cost Resource, price int, desc ...string) shopItem {
	return shopItem{Key: key, Label: label, Icon: icon, Count: count, Cost: cost, Price: price, Desc: desc, Kind: kindPlain}
}

// enchanted is a plain item carrying enchantments.
func enchanted(key, label, icon string, cost Resource, price int, ench map[string]int, desc ...string) shopItem {
	it := plain(key, label, icon, 1, cost, price, desc...)
	it.Stack = func(Team) game.ItemStack {
		return game.ItemStack{Item: icon, Count: 1, Enchantments: ench}
	}
	return it
}

// potion is a drinkable recognised by its display name in OnItemConsume.
func potion(key, label, potionID string, cost Resource, price int, desc ...string) shopItem {
	it := plain(key, label, "minecraft:potion", 1, cost, price, desc...)
	it.Stack = func(Team) game.ItemStack {
		return game.ItemStack{Item: "minecraft:potion", Count: 1, Name: label, Potion: potionID}
	}
	return it
}

// named is a plain item with a custom display name (bridge egg, pop-up
// tower) so OnItemUse can recognise it.
func named(key, label, icon string, count int, cost Resource, price int, desc ...string) shopItem {
	it := plain(key, label, icon, count, cost, price, desc...)
	it.Stack = func(Team) game.ItemStack {
		return game.ItemStack{Item: icon, Count: count, Name: label}
	}
	return it
}

// Display names of the custom utility items (matched in OnItemUse /
// OnItemConsume).
const (
	nameBridgeEgg  = "§aBridge Egg"
	namePopupTower = "§aCompact Pop-up Tower"
	nameSpeedPot   = "§bSpeed II Potion (45 seconds)"
	nameJumpPot    = "§aJump V Potion (45 seconds)"
	nameInvisPot   = "§7Invisibility Potion (30 seconds)"
	nameKBStick    = "§aKnockback Stick"
)

var shopCategories = []shopCategory{
	{Name: "Blocks", Icon: "minecraft:terracotta", Items: []shopItem{
		plain("wool", "§aWool", goodsWool, 16, Iron, 4, "Great for bridging across islands.", "Turns into your team's colour."),
		plain("terracotta", "§aHardened Clay", goodsTerracotta, 16, Iron, 12, "Sturdy block to protect your bed."),
		plain("glass", "§aBlast-Proof Glass", goodsGlass, 4, Iron, 12, "Immune to explosions."),
		plain("end_stone", "§aEnd Stone", "minecraft:end_stone", 12, Iron, 24, "Solid block to protect your bed."),
		plain("ladder", "§aLadder", "minecraft:ladder", 8, Iron, 4, "Useful to save cats stuck in trees."),
		plain("planks", "§aOak Wood", "minecraft:oak_planks", 16, Gold, 4, "Good block to protect your bed.", "Resistant to pickaxes."),
		plain("obsidian", "§aObsidian", "minecraft:obsidian", 4, Emerald, 4, "Extreme protection for your bed."),
	}},
	{Name: "Melee", Icon: "minecraft:golden_sword", Items: []shopItem{
		plain("stone_sword", "§aStone Sword", "minecraft:stone_sword", 1, Iron, 10),
		plain("iron_sword", "§aIron Sword", "minecraft:iron_sword", 1, Gold, 7),
		plain("diamond_sword", "§aDiamond Sword", "minecraft:diamond_sword", 1, Emerald, 4),
		enchanted("kb_stick", nameKBStick, "minecraft:stick", Gold, 5, map[string]int{"minecraft:knockback": 1},
			"Knock enemies off bridges and islands."),
	}},
	{Name: "Armor", Icon: "minecraft:chainmail_boots", Items: []shopItem{
		{Key: "chain_armor", Label: "§aPermanent Chainmail Armor", Icon: "minecraft:chainmail_boots", Count: 1, Cost: Iron, Price: 40,
			Desc: []string{"Chainmail leggings and boots that", "you keep after you die."}, Kind: kindArmor, Tier: armorChain},
		{Key: "iron_armor", Label: "§aPermanent Iron Armor", Icon: "minecraft:iron_boots", Count: 1, Cost: Gold, Price: 12,
			Desc: []string{"Iron leggings and boots that", "you keep after you die."}, Kind: kindArmor, Tier: armorIron},
		{Key: "diamond_armor", Label: "§aPermanent Diamond Armor", Icon: "minecraft:diamond_boots", Count: 1, Cost: Emerald, Price: 6,
			Desc: []string{"Diamond leggings and boots that", "you keep after you die."}, Kind: kindArmor, Tier: armorDiamond},
	}},
	{Name: "Tools", Icon: "minecraft:stone_pickaxe", Items: []shopItem{
		{Key: "shears", Label: "§aPermanent Shears", Icon: "minecraft:shears", Count: 1, Cost: Iron, Price: 20,
			Desc: []string{"Great to remove wool!", "You keep them after you die."}, Kind: kindPermanent,
			Stack: func(Team) game.ItemStack { return game.ItemStack{Item: "minecraft:shears", Count: 1} }},
		{Key: "pickaxe", Label: "§aPickaxe", Kind: kindTool, Tool: toolPickaxe,
			Desc: []string{"Upgradable: lose one tier when you die."}},
		{Key: "axe", Label: "§aAxe", Kind: kindTool, Tool: toolAxe,
			Desc: []string{"Upgradable: lose one tier when you die."}},
	}},
	{Name: "Ranged", Icon: "minecraft:bow", Items: []shopItem{
		plain("arrows", "§aArrow", "minecraft:arrow", 6, Gold, 2),
		plain("bow", "§aBow", "minecraft:bow", 1, Gold, 12),
		enchanted("bow_power", "§aBow (Power I)", "minecraft:bow", Gold, 24, map[string]int{"minecraft:power": 1}),
		enchanted("bow_power_punch", "§aBow (Power I, Punch I)", "minecraft:bow", Emerald, 6,
			map[string]int{"minecraft:power": 1, "minecraft:punch": 1}),
	}},
	{Name: "Potions", Icon: "minecraft:brewing_stand", Items: []shopItem{
		potion("speed_potion", nameSpeedPot, "minecraft:strong_swiftness", Emerald, 1, "Speed II for 45 seconds."),
		potion("jump_potion", nameJumpPot, "minecraft:strong_leaping", Emerald, 1, "Jump Boost V for 45 seconds."),
		potion("invis_potion", nameInvisPot, "minecraft:invisibility", Emerald, 2, "Complete invisibility for 30 seconds.", "Armor stays visible."),
	}},
	{Name: "Utility", Icon: "minecraft:tnt", Items: []shopItem{
		plain("golden_apple", "§aGolden Apple", "minecraft:golden_apple", 1, Gold, 3, "Well-rounded healing."),
		plain("fireball", "§aFireball", "minecraft:fire_charge", 1, Iron, 40, "Right-click to launch!", "Great to knock back enemies", "walking on thin bridges."),
		plain("tnt", "§aTNT", "minecraft:tnt", 1, Gold, 4, "Instantly ignites, perfect to", "blow up bed defences."),
		plain("ender_pearl", "§aEnder Pearl", "minecraft:ender_pearl", 1, Emerald, 4, "The quickest way to invade", "enemy bases."),
		plain("water_bucket", "§aWater Bucket", "minecraft:water_bucket", 1, Gold, 3, "Great to slow down approaching", "enemies, and to stop explosions."),
		named("bridge_egg", nameBridgeEgg, "minecraft:egg", 1, Emerald, 1, "Creates a bridge of your team's", "wool along its path."),
		plain("sponge", "§aSponge", "minecraft:sponge", 4, Gold, 3, "Great for soaking up water."),
		named("popup_tower", namePopupTower, "minecraft:chest", 1, Iron, 24, "Pops up a tower of your team's", "wool to escape or defend."),
	}},
}

// toolTier is one step of a pickaxe / axe upgrade ladder.
type toolTier struct {
	Item       string
	Cost       Resource
	Price      int
	Efficiency int
}

// toolTiers[toolPickaxe][tier-1] is the tier the player holds at kit tier
// `tier` (1..4). Buying advances to the next entry.
var toolTiers = map[toolKind][]toolTier{
	toolPickaxe: {
		{Item: "minecraft:wooden_pickaxe", Cost: Iron, Price: 10, Efficiency: 1},
		{Item: "minecraft:iron_pickaxe", Cost: Iron, Price: 10, Efficiency: 2},
		{Item: "minecraft:golden_pickaxe", Cost: Gold, Price: 3, Efficiency: 3},
		{Item: "minecraft:diamond_pickaxe", Cost: Gold, Price: 6, Efficiency: 3},
	},
	toolAxe: {
		{Item: "minecraft:wooden_axe", Cost: Iron, Price: 10, Efficiency: 1},
		{Item: "minecraft:stone_axe", Cost: Iron, Price: 10, Efficiency: 1},
		{Item: "minecraft:iron_axe", Cost: Gold, Price: 3, Efficiency: 2},
		{Item: "minecraft:diamond_axe", Cost: Gold, Price: 6, Efficiency: 3},
	},
}

// maxToolTier is the top of each tool ladder.
func maxToolTier(k toolKind) int { return len(toolTiers[k]) }

// toolStack builds the tool item for a tier (1-based); ok=false when out of
// range. Sharpened Swords doesn't apply to tools, so only Efficiency is set.
func toolStack(k toolKind, tier int) (game.ItemStack, bool) {
	tiers := toolTiers[k]
	if tier < 1 || tier > len(tiers) {
		return game.ItemStack{}, false
	}
	t := tiers[tier-1]
	return game.ItemStack{Item: t.Item, Count: 1, Enchantments: map[string]int{"minecraft:efficiency": t.Efficiency}}, true
}

// categoryByName finds a category index (-1 if unknown).
func categoryByName(name string) int {
	for i, c := range shopCategories {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// findShopItem looks an entry up by key across categories.
func findShopItem(key string) (shopItem, bool) {
	for _, c := range shopCategories {
		for _, it := range c.Items {
			if it.Key == key {
				return it, true
			}
		}
	}
	return shopItem{}, false
}

// priceLine renders "§7Cost: §f10 Iron".
func priceLine(cost Resource, price int) string {
	return fmt.Sprintf("§7Cost: %s%d %s", cost.colour(), price, cost.String())
}
