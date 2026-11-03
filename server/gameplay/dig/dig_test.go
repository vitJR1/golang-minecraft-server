package dig

import (
	"testing"

	"minecraft-server/world"
)

func TestAccepted(t *testing.T) {
	for _, tc := range []struct {
		needed  int
		elapsed uint64
		want    bool
	}{
		{10, 6, true}, // 7 ticks of 10 counted = 70%
		{10, 5, false},
		{0, 0, true},
		{-1, 1000, false}, // unbreakable
	} {
		if got := Accepted(tc.needed, tc.elapsed); got != tc.want {
			t.Errorf("Accepted(%d, %d) = %v, want %v", tc.needed, tc.elapsed, got, tc.want)
		}
	}
}

func TestDropAndWear(t *testing.T) {
	if item, _ := Drop(world.Stone, ""); item != "" {
		t.Errorf("stone by hand drops %q", item)
	}
	if item, n := Drop(world.Stone, "minecraft:wooden_pickaxe"); item != "minecraft:cobblestone" || n != 1 {
		t.Errorf("stone with a pickaxe drops %q×%d", item, n)
	}
	if w := ToolWear(world.Stone, "minecraft:iron_sword"); w != 2 {
		t.Errorf("sword wear %d", w)
	}
	if w := ToolWear(world.Stone, "minecraft:iron_pickaxe"); w != 1 {
		t.Errorf("pickaxe wear %d", w)
	}
}

func TestOverlaps(t *testing.T) {
	pos := world.Position{X: 0, Y: 64, Z: 0}
	if !Overlaps(pos, 0.5, 64, 0.5) || !Overlaps(pos, 1.2, 63, 0.5) {
		t.Error("a player standing in / reaching into the block overlaps it")
	}
	if Overlaps(pos, 1.3, 64, 0.5) || Overlaps(pos, 0.5, 65, 0.5) || Overlaps(pos, 0.5, 62.2, 0.5) {
		t.Error("a player beside, on top of or under the block doesn't")
	}
}
