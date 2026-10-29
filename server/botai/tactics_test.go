package botai

import (
	"testing"

	"minecraft-server/world"
)

func TestTNTBreaks(t *testing.T) {
	for name, want := range map[string]bool{
		"minecraft:obsidian": false, "minecraft:end_stone": true,
		"minecraft:white_wool": true, "minecraft:oak_planks": true,
	} {
		b, ok := world.BlockByName(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if got := TNTBreaks(b); got != want {
			t.Errorf("TNTBreaks(%s) = %v, want %v", name, got, want)
		}
	}
}
