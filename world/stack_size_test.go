package world

import "testing"

func TestMaxStackSizeOf(t *testing.T) {
	cases := map[string]int{
		"minecraft:white_wool":   64,
		"minecraft:iron_ingot":   64,
		"minecraft:ender_pearl":  16,
		"minecraft:egg":          16,
		"minecraft:stone_sword":  1,
		"minecraft:iron_boots":   1,
		"minecraft:potion":       1,
		"minecraft:water_bucket": 1,
		"minecraft:shears":       1,
		"minecraft:never_heard":  64,
	}
	for item, want := range cases {
		if got := MaxStackSizeOf(item); got != want {
			t.Errorf("%s: got %d, want %d", item, got, want)
		}
	}
}
