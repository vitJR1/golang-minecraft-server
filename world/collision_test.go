package world

import "testing"

func TestCollisionClassification(t *testing.T) {
	block := func(name string) Block { return Block{StateID: 1, Name: name} }
	full := []string{"minecraft:stone", "minecraft:light_blue_wool", "minecraft:sea_lantern",
		"minecraft:crafting_table", "minecraft:oak_planks", "minecraft:end_stone", "minecraft:obsidian",
		"minecraft:red_terracotta", "minecraft:glass", "minecraft:oak_leaves", "minecraft:iron_block"}
	for _, n := range full {
		if !FullCube(block(n)) || !Collidable(block(n)) {
			t.Errorf("%s should be a full, collidable cube", n)
		}
	}
	partial := []string{"minecraft:oak_slab", "minecraft:stone_stairs", "minecraft:oak_fence",
		"minecraft:cobblestone_wall", "minecraft:red_bed", "minecraft:chest", "minecraft:ladder",
		"minecraft:soul_sand", "minecraft:snow", "minecraft:honey_block", "minecraft:glass_pane",
		"minecraft:oak_door", "minecraft:white_carpet", "minecraft:enchanting_table"}
	for _, n := range partial {
		if FullCube(block(n)) {
			t.Errorf("%s isn't a full cube", n)
		}
		if !Collidable(block(n)) {
			t.Errorf("%s has collision (it holds a player up)", n)
		}
	}
	passable := []string{"minecraft:air", "minecraft:water", "minecraft:lava", "minecraft:torch",
		"minecraft:wall_torch", "minecraft:oak_sign", "minecraft:poppy", "minecraft:grass",
		"minecraft:rail", "minecraft:stone_button", "minecraft:light"}
	for _, n := range passable {
		b := block(n)
		if n == "minecraft:air" {
			b = Air
		}
		if Collidable(b) || FullCube(b) {
			t.Errorf("%s has no collision", n)
		}
	}
	if !Collidable(block("minecraft:mangrove_roots")) {
		t.Error("mangrove_roots is solid")
	}
}
