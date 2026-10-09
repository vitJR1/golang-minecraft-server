package bedwars

import (
	"strings"
	"testing"

	"minecraft-server/game"
	"minecraft-server/world"
)

func openShopFor(t *testing.T) (*bedWars, *fakePlayer) {
	t.Helper()
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1) // first joiner → team 0 (Red)
	villager := game.EntityInteraction{EntityID: 900, Type: "minecraft:villager"}
	if g.OnEntityInteract(ctx, red, villager) {
		t.Fatal("villager click should be consumed")
	}
	if red.menuTitle == "" || len(red.menuItems) != len(shopOffers) {
		t.Fatalf("shop menu not opened: title=%q items=%d", red.menuTitle, len(red.menuItems))
	}
	return g, red
}

func TestShopOpensWithTeamColouredGoods(t *testing.T) {
	_, red := openShopFor(t)
	if red.menuItems[0].Item != "minecraft:red_wool" || red.menuItems[0].Count != 16 {
		t.Errorf("wool offer: %+v", red.menuItems[0])
	}
	if red.menuItems[2].Item != "minecraft:red_terracotta" || red.menuItems[2].Count != 8 {
		t.Errorf("terracotta offer: %+v", red.menuItems[2])
	}
	if !strings.Contains(red.menuItems[4].Name, "6 Emerald") {
		t.Errorf("obsidian label: %q", red.menuItems[4].Name)
	}
}

func TestShopBuyTakesPriceAndGivesGoods(t *testing.T) {
	_, red := openShopFor(t)
	red.GiveItem("minecraft:iron_ingot", 30)
	red.GiveItem("minecraft:gold_ingot", 8)
	red.GiveItem("minecraft:emerald", 5)

	red.menuClick(0) // wool: 16 for 4 iron
	red.menuClick(2) // terracotta: 8 for 24 iron
	red.menuClick(3) // end stone: 8 for 8 gold
	red.menuClick(4) // obsidian: 6 emerald — only 5 → refused

	if got := red.CountItem("minecraft:red_wool"); got != 16 {
		t.Errorf("wool: %d, want 16", got)
	}
	if got := red.CountItem("minecraft:red_terracotta"); got != 8 {
		t.Errorf("terracotta: %d, want 8", got)
	}
	if got := red.CountItem("minecraft:iron_ingot"); got != 2 {
		t.Errorf("iron left: %d, want 30-4-24=2", got)
	}
	if got := red.CountItem("minecraft:end_stone"); got != 8 {
		t.Errorf("end stone: %d, want 8", got)
	}
	if got := red.CountItem("minecraft:gold_ingot"); got != 0 {
		t.Errorf("gold left: %d, want 0", got)
	}
	if got := red.CountItem("minecraft:obsidian"); got != 0 {
		t.Errorf("obsidian must not be sold for 5 emerald, got %d", got)
	}
	if got := red.CountItem("minecraft:emerald"); got != 5 {
		t.Errorf("emerald untouched: %d, want 5", got)
	}
	last := red.messages[len(red.messages)-1]
	if !strings.Contains(last, "Not enough Emerald") {
		t.Errorf("refusal message: %q", last)
	}

	// Second team gets its own colour.
	// (Blue is config index 1 in the generated 2+ team arena.)
	if resolveGoods("terracotta", buildTeams(2)[1]) != "minecraft:blue_terracotta" {
		t.Error("blue terracotta resolution")
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
