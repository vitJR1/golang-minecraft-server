package server

import (
	"testing"

	"minecraft-server/world"
)

func TestLadderHangsOnTheClickedWall(t *testing.T) {
	s := New()
	inst := NewInstance("t", s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	wall := world.Position{X: 0, Y: 64, Z: 0}
	inst.World.SetBlock(wall, world.Stone)

	// Clicking the wall's south face (3) puts the ladder at z+1, facing south.
	pos := world.Position{X: 0, Y: 64, Z: 1}
	blk, ok := inst.orientForPlacement(world.Ladder, pos, 3, 0)
	if !ok {
		t.Fatal("ladder should attach to the clicked wall")
	}
	want := world.ResolveStateID("minecraft:ladder", map[string]string{"facing": "south", "waterlogged": "false"})
	if blk.StateID != want {
		t.Errorf("state %d, want %d (facing south)", blk.StateID, want)
	}

	// Clicking the top of the floor next to the wall: the ladder picks the
	// wall the player faces (yaw 180 = looking north, wall is north of pos).
	blk, ok = inst.orientForPlacement(world.Ladder, pos, 1, 180)
	if !ok || blk.StateID != want {
		t.Errorf("top-face placement should still find the wall: ok=%v state=%d", ok, blk.StateID)
	}

	// No wall anywhere → can't place.
	if _, ok := inst.orientForPlacement(world.Ladder, world.Position{X: 10, Y: 64, Z: 10}, 1, 0); ok {
		t.Error("ladder in mid-air must be rejected")
	}

	// Other blocks pass through untouched.
	if blk, ok := inst.orientForPlacement(world.Stone, pos, 1, 0); !ok || blk != world.Stone {
		t.Error("stone must not be touched by orientation rules")
	}
}
