package world

import "testing"

func TestBreakTicksMatchesVanilla(t *testing.T) {
	cases := []struct {
		block, item string
		want        int
	}{
		{"minecraft:stone", "", 150},                        // 7.5s by hand (no drop)
		{"minecraft:stone", "minecraft:wooden_pickaxe", 23}, // 1.15s
		{"minecraft:stone", "minecraft:diamond_pickaxe", 6},
		{"minecraft:obsidian", "minecraft:diamond_pickaxe", 188}, // 9.4s
		{"minecraft:obsidian", "minecraft:iron_pickaxe", 834},    // wrong tier: 41.7s
		{"minecraft:red_wool", "", 24},                           // 1.2s by hand
		{"minecraft:red_wool", "minecraft:shears", 2},
		{"minecraft:oak_planks", "", 60},                   // 3s
		{"minecraft:oak_planks", "minecraft:iron_axe", 10}, // 0.5s
		{"minecraft:red_terracotta", "minecraft:wooden_pickaxe", 19},
		{"minecraft:end_stone", "minecraft:iron_pickaxe", 15},
		{"minecraft:dirt", "", 15},
		{"minecraft:torch", "", 0},
		{"minecraft:bedrock", "minecraft:netherite_pickaxe", -1},
	}
	for _, c := range cases {
		got := BreakTicks(BreakInfoFor(c.block), c.item)
		if got != c.want {
			t.Errorf("%s with %q: %d ticks, want %d", c.block, c.item, got, c.want)
		}
	}
}

func TestCanHarvestAndDrops(t *testing.T) {
	if CanHarvest(BreakInfoFor("minecraft:stone"), "") {
		t.Error("stone by hand must not drop")
	}
	if !CanHarvest(BreakInfoFor("minecraft:stone"), "minecraft:wooden_pickaxe") {
		t.Error("stone with a wooden pickaxe drops")
	}
	if CanHarvest(BreakInfoFor("minecraft:obsidian"), "minecraft:iron_pickaxe") {
		t.Error("obsidian needs diamond")
	}
	if !CanHarvest(BreakInfoFor("minecraft:red_wool"), "") {
		t.Error("wool drops by hand")
	}
	if !CanHarvest(BreakInfoFor("minecraft:oak_planks"), "minecraft:stone_pickaxe") {
		t.Error("planks drop with any tool")
	}
	if item, n := BlockDrop("minecraft:stone"); item != "minecraft:cobblestone" || n != 1 {
		t.Errorf("stone drop: %s×%d", item, n)
	}
	if item, _ := BlockDrop("minecraft:oak_leaves"); item != "" {
		t.Errorf("leaves drop: %q", item)
	}
	if item, n := BlockDrop("minecraft:red_wool"); item != "minecraft:red_wool" || n != 1 {
		t.Errorf("wool drop: %s×%d", item, n)
	}
	if ToolFromItem("minecraft:golden_axe") != (Tool{Axe, TierWood}) {
		t.Error("golden axe parse")
	}
	if ToolFromItem("minecraft:iron_ingot") != (Tool{}) {
		t.Error("ingot is not a tool")
	}
}

func TestBreakTicksWithModifiers(t *testing.T) {
	stone := BreakInfoFor("minecraft:stone")
	base := DigContext{OnGround: true}
	if got := BreakTicksWith(stone, "minecraft:diamond_pickaxe", base); got != 6 {
		t.Fatalf("baseline: %d", got)
	}
	// Efficiency V on diamond: speed 8 + 26 = 34 → 34/1.5/30 = 0.756 → 2 ticks.
	if got := BreakTicksWith(stone, "minecraft:diamond_pickaxe", DigContext{Efficiency: 5, OnGround: true}); got != 2 {
		t.Errorf("efficiency V: %d, want 2", got)
	}
	// Efficiency on the wrong tool class does nothing.
	if got := BreakTicksWith(stone, "minecraft:diamond_axe", DigContext{Efficiency: 5, OnGround: true}); got != 150 {
		t.Errorf("efficiency on axe vs stone: %d, want 150", got)
	}
	// Haste II: ×1.4 → 8*1.4/1.5/30 = 0.249 → 5 ticks.
	if got := BreakTicksWith(stone, "minecraft:diamond_pickaxe", DigContext{Haste: 2, OnGround: true}); got != 5 {
		t.Errorf("haste II: %d, want 5", got)
	}
	// Mining Fatigue I: ×0.3 → 0.0533 → 19 ticks.
	if got := BreakTicksWith(stone, "minecraft:diamond_pickaxe", DigContext{MiningFatigue: 1, OnGround: true}); got != 19 {
		t.Errorf("fatigue I: %d, want 19", got)
	}
	// Underwater and airborne: ÷5 each → 6 → 29 → 141.
	if got := BreakTicksWith(stone, "minecraft:diamond_pickaxe", DigContext{InWater: true, OnGround: true}); got != 29 {
		t.Errorf("in water: %d, want 29", got)
	}
	if got := BreakTicksWith(stone, "minecraft:diamond_pickaxe", DigContext{InWater: true, AquaAffinity: true, OnGround: true}); got != 6 {
		t.Errorf("aqua affinity: %d, want 6", got)
	}
	if got := BreakTicksWith(stone, "minecraft:diamond_pickaxe", DigContext{InWater: true, OnGround: false}); got != 141 {
		t.Errorf("in water + airborne: %d, want 141", got)
	}
	if ToolDurability("minecraft:diamond_pickaxe") != 1561 || ToolDurability("minecraft:golden_axe") != 32 ||
		ToolDurability("minecraft:shears") != 238 || ToolDurability("minecraft:stone") != 0 {
		t.Error("durability table")
	}
}

func TestBlastResistance(t *testing.T) {
	cases := map[string]float64{
		"minecraft:obsidian":   1200,
		"minecraft:stone":      6,
		"minecraft:red_wool":   0.8,
		"minecraft:oak_planks": 3,
		"minecraft:tnt":        0,
		"minecraft:glass":      0.3,
		"minecraft:red_bed":    0.2,
		"minecraft:water":      100,
	}
	for name, want := range cases {
		if got := BlastResistance(name); got != want {
			t.Errorf("BlastResistance(%s) = %v, want %v", name, got, want)
		}
	}
	if got := BlastResistance("minecraft:bedrock"); got < 1e6 {
		t.Errorf("bedrock should be effectively indestructible, got %v", got)
	}
}
