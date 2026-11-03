package fluid

import (
	"testing"

	"minecraft-server/world"
)

// TestRayToPlacementSkipsEyeBlock: a player standing in a ladder (head in
// the ladder's cell) empties a bucket at the wall in front; the water goes
// in front of the wall, never over the ladder (review #27).
func TestRayToPlacementSkipsEyeBlock(t *testing.T) {
	ladder := world.Position{X: 0, Y: 65, Z: 0}
	wall := world.Position{X: 0, Y: 65, Z: 3}
	at := func(p world.Position) world.Block {
		switch p {
		case ladder:
			return world.Ladder
		case wall:
			return world.Stone
		}
		return world.Air
	}
	// Eyes at (0.5, 65.6, 0.5) looking +Z.
	got, ok := RayToPlacement(at, 0.5, 65.6, 0.5, 0, 0, 1)
	if !ok || got != (world.Position{X: 0, Y: 65, Z: 2}) {
		t.Fatalf("placement = %v, %v; want the air cell before the wall", got, ok)
	}

	// The ladder's cell is the only one before a wall right against it:
	// nothing may be placed.
	at2 := func(p world.Position) world.Block {
		switch p {
		case ladder:
			return world.Ladder
		case world.Position{X: 0, Y: 65, Z: 1}:
			return world.Stone
		}
		return world.Air
	}
	if got, ok := RayToPlacement(at2, 0.5, 65.6, 0.5, 0, 0, 1); ok {
		t.Fatalf("placed fluid at %v over the ladder the eyes are in", got)
	}

	// Plain case unchanged: open air, wall two blocks away.
	open := func(p world.Position) world.Block {
		if p == wall {
			return world.Stone
		}
		return world.Air
	}
	if got, ok := RayToPlacement(open, 0.5, 65.6, 0.5, 0, 0, 1); !ok || got != (world.Position{X: 0, Y: 65, Z: 2}) {
		t.Fatalf("open placement = %v, %v", got, ok)
	}
}
