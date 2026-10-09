package world

import "testing"

func TestAttackDamageTable(t *testing.T) {
	cases := map[string]float32{
		"": 1, "minecraft:stone": 1, "minecraft:iron_ingot": 1,
		"minecraft:wooden_sword": 4, "minecraft:golden_sword": 4, "minecraft:stone_sword": 5,
		"minecraft:iron_sword": 6, "minecraft:diamond_sword": 7, "minecraft:netherite_sword": 8,
		"minecraft:wooden_axe": 7, "minecraft:stone_axe": 9, "minecraft:iron_axe": 9, "minecraft:diamond_axe": 9,
		"minecraft:iron_pickaxe": 1,
	}
	for item, want := range cases {
		if got := AttackDamage(item); got != want {
			t.Errorf("AttackDamage(%q) = %v, want %v", item, got, want)
		}
	}
}

func TestArmorPointsAndDurability(t *testing.T) {
	if p, tg := ArmorPoints("minecraft:diamond_chestplate"); p != 8 || tg != 2 {
		t.Errorf("diamond chestplate: %v/%v", p, tg)
	}
	if p, tg := ArmorPoints("minecraft:leather_boots"); p != 1 || tg != 0 {
		t.Errorf("leather boots: %v/%v", p, tg)
	}
	if p, _ := ArmorPoints("minecraft:chainmail_leggings"); p != 4 {
		t.Errorf("chainmail leggings: %v", p)
	}
	if p, _ := ArmorPoints("minecraft:iron_sword"); p != 0 {
		t.Error("a sword has no armor points")
	}
	for item, want := range map[string]int{
		"minecraft:leather_helmet": 55, "minecraft:iron_chestplate": 240, "minecraft:golden_leggings": 105,
		"minecraft:diamond_boots": 429, "minecraft:netherite_chestplate": 592, "minecraft:diamond_pickaxe": 1561,
	} {
		if got := ToolDurability(item); got != want {
			t.Errorf("ToolDurability(%q) = %d, want %d", item, got, want)
		}
	}
}
