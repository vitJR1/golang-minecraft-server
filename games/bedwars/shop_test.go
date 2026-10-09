package bedwars

import (
	"strings"
	"testing"

	"minecraft-server/game"
	"minecraft-server/world"
)

// openShopFor joins a red player and opens the item shop through a villager
// click (no arena villager table → item shop by default).
func openShopFor(t *testing.T) (*bedWars, *fakePlayer) {
	t.Helper()
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1) // first joiner → team 0 (Red)
	villager := game.EntityInteraction{EntityID: 900, Type: "minecraft:villager", X: 100.5, Y: 70, Z: 100.5}
	if g.OnEntityInteract(ctx, red, villager) {
		t.Fatal("villager click should be consumed")
	}
	if red.menuTitle == "" || red.menuClick == nil {
		t.Fatalf("shop menu not opened: title=%q", red.menuTitle)
	}
	return g, red
}

// menuAt returns the menu item in a slot (nil if empty).
func menuAt(p *fakePlayer, slot int) *game.MenuItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.menuItems {
		if p.menuItems[i].Slot == slot {
			return &p.menuItems[i]
		}
	}
	return nil
}

// openCategory clicks the tab for the named category.
func openCategory(t *testing.T, p *fakePlayer, name string) {
	t.Helper()
	idx := categoryByName(name)
	if idx < 0 {
		t.Fatalf("no category %q", name)
	}
	p.menuClick(shopTabSlot0 + idx)
	if !strings.Contains(p.menuTitle, name) {
		t.Fatalf("category %q not opened: title=%q", name, p.menuTitle)
	}
}

// buyKey clicks the ware with the given catalogue key in the open category.
func buyKey(t *testing.T, p *fakePlayer, key string) {
	t.Helper()
	cat := -1
	for i, c := range shopCategories {
		if strings.Contains(p.menuTitle, c.Name) {
			cat = i
		}
	}
	if cat < 0 {
		t.Fatalf("no category open: %q", p.menuTitle)
	}
	for i, it := range shopCategories[cat].Items {
		if it.Key == key {
			p.menuClick(wareSlots[i])
			return
		}
	}
	t.Fatalf("key %q not in category %s", key, shopCategories[cat].Name)
}

func TestShopLayoutAndTeamColours(t *testing.T) {
	_, red := openShopFor(t)
	// Tabs across the top, Blocks selected (glint), separators below.
	for i, c := range shopCategories {
		tab := menuAt(red, shopTabSlot0+i)
		if tab == nil || tab.Item != c.Icon {
			t.Fatalf("tab %d (%s) = %+v", i, c.Name, tab)
		}
		if tab.Glint != (i == 0) {
			t.Errorf("tab %s glint=%v", c.Name, tab.Glint)
		}
	}
	if sep := menuAt(red, shopSepRow); sep == nil || sep.Item != shopSeparator {
		t.Errorf("separator row: %+v", sep)
	}
	wool := menuAt(red, wareSlots[0])
	if wool == nil || wool.Item != "minecraft:red_wool" || wool.Count != 16 {
		t.Fatalf("wool ware: %+v", wool)
	}
	if !strings.Contains(strings.Join(wool.Lore, "\n"), "Cost: §f4 Iron") {
		t.Errorf("wool lore lacks the price: %v", wool.Lore)
	}
	if !strings.Contains(strings.Join(wool.Lore, "\n"), "don't have enough Iron") {
		t.Errorf("wool lore should say the player can't afford it: %v", wool.Lore)
	}
	if glass := menuAt(red, wareSlots[2]); glass == nil || glass.Item != "minecraft:red_stained_glass" {
		t.Errorf("glass ware: %+v", glass)
	}
	if resolveGoods(goodsTerracotta, buildTeams(2)[1]) != "minecraft:blue_terracotta" {
		t.Error("blue terracotta resolution")
	}
}

func TestShopCategoriesSwitch(t *testing.T) {
	_, red := openShopFor(t)
	openCategory(t, red, "Melee")
	if sw := menuAt(red, wareSlots[0]); sw == nil || sw.Item != "minecraft:stone_sword" {
		t.Errorf("first melee ware: %+v", sw)
	}
	if tab := menuAt(red, shopTabSlot0+categoryByName("Melee")); tab == nil || !tab.Glint {
		t.Error("selected tab should shimmer")
	}
	openCategory(t, red, "Utility")
	if fb := menuAt(red, wareSlots[1]); fb == nil || fb.Item != "minecraft:fire_charge" {
		t.Errorf("fireball ware: %+v", fb)
	}
}

func TestShopBuyPlainGoods(t *testing.T) {
	_, red := openShopFor(t)
	red.GiveItem("minecraft:iron_ingot", 30)
	red.GiveItem("minecraft:gold_ingot", 8)
	red.GiveItem("minecraft:emerald", 3)

	buyKey(t, red, "wool")       // 16 for 4 iron
	buyKey(t, red, "terracotta") // 16 for 12 iron
	buyKey(t, red, "planks")     // 16 for 4 gold
	buyKey(t, red, "obsidian")   // 4 emerald — only 3 → refused

	if got := red.CountItem("minecraft:red_wool"); got != 16 {
		t.Errorf("wool: %d, want 16", got)
	}
	if got := red.CountItem("minecraft:red_terracotta"); got != 16 {
		t.Errorf("terracotta: %d, want 16", got)
	}
	if got := red.CountItem("minecraft:iron_ingot"); got != 14 {
		t.Errorf("iron left: %d, want 30-4-12=14", got)
	}
	if got := red.CountItem("minecraft:oak_planks"); got != 16 {
		t.Errorf("planks: %d, want 16", got)
	}
	if got := red.CountItem("minecraft:obsidian"); got != 0 {
		t.Errorf("obsidian must not be sold for 3 emerald, got %d", got)
	}
	last := red.messages[len(red.messages)-1]
	if !strings.Contains(last, "don't have enough Emerald") {
		t.Errorf("refusal message: %q", last)
	}
	// The menu was rebuilt after the purchases and now says "Click to purchase!" for wool.
	if wool := menuAt(red, wareSlots[0]); wool == nil || !strings.Contains(strings.Join(wool.Lore, "\n"), "Click to purchase") {
		t.Errorf("wool lore after buying: %+v", wool)
	}
}

func TestShopSwordsCarryTeamSharpness(t *testing.T) {
	g, red := openShopFor(t)
	red.GiveItem("minecraft:iron_ingot", 10)
	openCategory(t, red, "Melee")
	buyKey(t, red, "stone_sword")
	sword := findItem(red, "minecraft:stone_sword")
	if sword == nil || sword.Enchantments["minecraft:sharpness"] != 0 {
		t.Fatalf("plain stone sword expected: %+v", sword)
	}
	g.mu.Lock()
	g.teams[0].sharpened = true
	g.mu.Unlock()
	red.GiveItem("minecraft:iron_ingot", 10)
	buyKey(t, red, "stone_sword")
	n := 0
	for _, st := range red.Inventory() {
		if st.Item == "minecraft:stone_sword" && st.Enchantments["minecraft:sharpness"] == 1 {
			n++
		}
	}
	if n != 1 {
		t.Errorf("one Sharpness I stone sword expected after the upgrade, got %d", n)
	}
}

func TestShopArmorTiersArePermanentAndOrdered(t *testing.T) {
	g, red := openShopFor(t)
	openCategory(t, red, "Armor")
	red.GiveItem("minecraft:gold_ingot", 12)
	buyKey(t, red, "iron_armor")
	if red.slot(game.SlotLeggings).Item != "minecraft:iron_leggings" || red.slot(game.SlotBoots).Item != "minecraft:iron_boots" {
		t.Fatalf("iron armour not equipped: %+v / %+v", red.slot(game.SlotLeggings), red.slot(game.SlotBoots))
	}
	g.mu.Lock()
	tier := g.kits[red.eid].armor
	g.mu.Unlock()
	if tier != armorIron {
		t.Errorf("kit armour tier = %d, want iron", tier)
	}
	// Chainmail is now worse: refused, money untouched.
	red.GiveItem("minecraft:iron_ingot", 40)
	buyKey(t, red, "chain_armor")
	if red.CountItem("minecraft:iron_ingot") != 40 {
		t.Error("buying a lower armour tier must not charge")
	}
	if ware := menuAt(red, wareSlots[0]); ware == nil || !strings.Contains(strings.Join(ware.Lore, "\n"), "Already owned") {
		t.Errorf("chainmail lore should say already owned: %+v", ware)
	}
	// Helmet/chestplate are leather in the team colour.
	if helm := red.slot(game.SlotHelmet); helm.Item != "minecraft:leather_helmet" || helm.Color != buildTeams(1)[0].Color {
		t.Errorf("helmet: %+v", helm)
	}
}

func TestShopToolTiersAdvanceAndDowngradeOnDeath(t *testing.T) {
	g, red := openShopFor(t)
	openCategory(t, red, "Tools")
	red.GiveItem("minecraft:iron_ingot", 20)
	buyKey(t, red, "pickaxe") // wood, 10 iron
	if p := findItem(red, "minecraft:wooden_pickaxe"); p == nil || p.Enchantments["minecraft:efficiency"] != 1 {
		t.Fatalf("wooden pickaxe (Efficiency I) expected: %+v", p)
	}
	buyKey(t, red, "pickaxe") // iron, 10 iron — replaces the wooden one in place
	if findItem(red, "minecraft:wooden_pickaxe") != nil || findItem(red, "minecraft:iron_pickaxe") == nil {
		t.Fatal("iron pickaxe should replace the wooden one")
	}
	if ware := menuAt(red, wareSlots[1]); ware == nil || ware.Item != "minecraft:golden_pickaxe" {
		t.Errorf("next tier shown should be golden: %+v", ware)
	}
	// Death: tier drops to wood, and the kit re-equips it.
	g.mu.Lock()
	k := g.kits[red.eid]
	g.mu.Unlock()
	k.onDeath()
	if k.pickaxe != 1 {
		t.Errorf("pickaxe tier after death = %d, want 1", k.pickaxe)
	}
	k.onDeath()
	if k.pickaxe != 1 {
		t.Error("a bought pickaxe never drops below wood")
	}
}

func TestShopPermanentShears(t *testing.T) {
	_, red := openShopFor(t)
	openCategory(t, red, "Tools")
	red.GiveItem("minecraft:iron_ingot", 40)
	buyKey(t, red, "shears")
	buyKey(t, red, "shears")
	if red.CountItem("minecraft:shears") != 1 || red.CountItem("minecraft:iron_ingot") != 20 {
		t.Errorf("shears should be bought once: shears=%d iron=%d", red.CountItem("minecraft:shears"), red.CountItem("minecraft:iron_ingot"))
	}
}

func TestShopPotionsAndNamedUtilities(t *testing.T) {
	_, red := openShopFor(t)
	red.GiveItem("minecraft:emerald", 3)
	red.GiveItem("minecraft:iron_ingot", 24)
	openCategory(t, red, "Potions")
	buyKey(t, red, "speed_potion")
	pot := findItem(red, "minecraft:potion")
	if pot == nil || pot.Name != nameSpeedPot || pot.Potion != "minecraft:strong_swiftness" {
		t.Fatalf("speed potion: %+v", pot)
	}
	openCategory(t, red, "Utility")
	buyKey(t, red, "bridge_egg")
	buyKey(t, red, "popup_tower")
	if egg := findItem(red, "minecraft:egg"); egg == nil || egg.Name != nameBridgeEgg {
		t.Errorf("bridge egg: %+v", egg)
	}
	if tower := findItem(red, "minecraft:chest"); tower == nil || tower.Name != namePopupTower {
		t.Errorf("popup tower: %+v", tower)
	}
}

func TestVillagerKindFromArenaTable(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	pos := world.Position{X: 7, Y: 65, Z: 7}
	g.arena.Villagers = map[world.Position]villagerSpot{pos: {Pos: pos, Team: 0, Kind: villagerUpgrade}}
	click := game.EntityInteraction{EntityID: 901, Type: "minecraft:villager", X: 7.5, Y: 65, Z: 7.5}
	if g.OnEntityInteract(ctx, red, click) {
		t.Fatal("upgrade villager click should be consumed")
	}
	if strings.Contains(red.menuTitle, "Item Shop") {
		t.Error("upgrade villager must not open the item shop")
	}
}

func TestShopIgnoresNonVillagers(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	if !g.OnEntityInteract(ctx, red, game.EntityInteraction{EntityID: 1, Type: "minecraft:item_frame"}) {
		t.Error("item frame click must pass through")
	}
	if red.menuTitle != "" {
		t.Error("no menu for item frames")
	}
}

func TestPlacedBedsTakeTheTeamColour(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	blue := join(g, inst, ctx, "blue", 2)
	pos := world.Position{X: 3, Y: 65, Z: 3}
	if got := g.RewritePlacedBlock(ctx, red, pos, world.YellowBed); got != world.RedBed {
		t.Errorf("red player placing a yellow bed: %+v, want RedBed", got)
	}
	if got := g.RewritePlacedBlock(ctx, blue, pos, world.YellowBed); got != world.BlueBed {
		t.Errorf("blue player placing a yellow bed: %+v, want BlueBed", got)
	}
	if got := g.RewritePlacedBlock(ctx, red, pos, world.Stone); got != world.Stone {
		t.Error("non-bed blocks must pass through")
	}
}

// findItem returns the first inventory stack of the item (nil if none).
func findItem(p *fakePlayer, item string) *game.ItemStack {
	for _, st := range p.Inventory() {
		if st.Item == item {
			s := st
			return &s
		}
	}
	return nil
}
