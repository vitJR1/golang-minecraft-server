package server

import (
	"testing"

	"minecraft-server/world"
)

func at(x, y, z int) world.Position { return world.Position{X: x, Y: y, Z: z} }

// reach is a goal: exactly that cell.
func reach(p world.Position) func(world.Position) bool {
	return func(c world.Position) bool { return c == p }
}

func bridgesIn(path []routeStep) int {
	n := 0
	for _, s := range path {
		if s.Bridge {
			n++
		}
	}
	return n
}

func TestPathWalksAroundAWall(t *testing.T) {
	w := testWorld{}
	w.floor(63, -5, 10, -5, 5)
	for z := -3; z <= 3; z++ { // a wall at x=3 with a way round at z=±4
		w[at(3, 64, z)] = world.Stone
		w[at(3, 65, z)] = world.Stone
	}
	path := findPath(w.at, at(0, 64, 0), at(6, 64, 0), reach(at(6, 64, 0)), 0, pathNearNodes)
	if path == nil {
		t.Fatal("no way round the wall")
	}
	for _, s := range path {
		if s.Cell.X == 3 && s.Cell.Z >= -3 && s.Cell.Z <= 3 {
			t.Fatalf("path goes through the wall at %v", s.Cell)
		}
		if s.Bridge {
			t.Fatal("bridged when walking round was possible")
		}
	}
}

func TestPathBridgesAGap(t *testing.T) {
	w := testWorld{}
	w.floor(63, -3, 2, -2, 2) // island A
	w.floor(63, 8, 12, -2, 2) // island B, a 5-block gap at x=3..7
	path := findPath(w.at, at(0, 64, 0), at(10, 64, 0), reach(at(10, 64, 0)), 64, pathNearNodes)
	if path == nil {
		t.Fatal("no bridge across the gap")
	}
	if n := bridgesIn(path); n != 5 {
		t.Fatalf("%d bridge steps, want 5 (the gap)", n)
	}
	if findPath(w.at, at(0, 64, 0), at(10, 64, 0), reach(at(10, 64, 0)), 3, pathNearNodes) != nil {
		t.Fatal("3 blocks were enough for a 5-block gap")
	}
	if findPath(w.at, at(0, 64, 0), at(10, 64, 0), reach(at(10, 64, 0)), 0, pathNearNodes) != nil {
		t.Fatal("crossed the void without blocks")
	}
}

func TestPathJumpsUpAndDropsDown(t *testing.T) {
	w := testWorld{}
	w.floor(63, -2, 10, -2, 2)
	w.floor(64, 3, 5, -2, 2) // a one-block step up at x=3..5
	path := findPath(w.at, at(0, 64, 0), at(8, 64, 0), reach(at(8, 64, 0)), 0, pathNearNodes)
	if path == nil {
		t.Fatal("no path over the step")
	}
	up := false
	for _, s := range path {
		if s.Cell.Y == 65 {
			up = true
		}
	}
	if !up {
		t.Fatal("path didn't climb the step")
	}

	tall := testWorld{}
	tall.floor(63, -2, 10, -2, 2)
	tall.floor(64, 3, 3, -2, 2)
	tall.floor(65, 3, 3, -2, 2) // a two-block wall: no jumping it
	if p := findPath(tall.at, at(0, 64, 0), at(8, 64, 0), reach(at(8, 64, 0)), 0, pathNearNodes); p != nil {
		t.Fatalf("jumped a two-block wall: %v", p)
	}
}

func TestPathDropsFromALedge(t *testing.T) {
	w := testWorld{}
	w.floor(66, -2, 2, -2, 2) // a ledge 3 blocks above the ground
	w.floor(63, 3, 8, -2, 2)
	path := findPath(w.at, at(0, 67, 0), at(6, 64, 0), reach(at(6, 64, 0)), 0, pathNearNodes)
	if path == nil || bridgesIn(path) != 0 {
		t.Fatalf("no drop off the ledge: %v", path)
	}
}

// TestPathDoesntClimbWalls: a wall / fence is 1.5 blocks tall — beyond a
// jump — so the path goes round it, never over.
func TestPathDoesntClimbWalls(t *testing.T) {
	wall, ok := world.BlockByName("minecraft:mossy_stone_brick_wall")
	if !ok {
		t.Fatal("mossy_stone_brick_wall not registered")
	}
	w := testWorld{}
	w.floor(63, -2, 10, -4, 4)
	for z := -2; z <= 2; z++ {
		w[at(3, 64, z)] = wall
	}
	path := findPath(w.at, at(0, 64, 0), at(6, 64, 0), reach(at(6, 64, 0)), 0, pathNearNodes)
	if path == nil {
		t.Fatal("no way round the wall")
	}
	for _, s := range path {
		if s.Cell.Y > 64 {
			t.Fatalf("path climbs onto the wall at %v", s.Cell)
		}
	}
}

// TestRouteUsesAnExistingBridge: with a bridge already across the gap, the
// route walks it instead of laying a new one straight at the target.
func TestRouteUsesAnExistingBridge(t *testing.T) {
	w := testWorld{}
	w.floor(63, -3, 2, -3, 3) // island A
	w.floor(63, 8, 12, -3, 3) // island B
	for x := 3; x <= 7; x++ { // an old bridge along z=-3
		w[at(x, 63, -3)] = world.Stone
	}
	goal := reach(at(10, 64, 2))
	greedy := findPath(w.at, at(0, 64, 2), at(10, 64, 2), goal, 64, pathNearNodes)
	if bridgesIn(greedy) == 0 {
		t.Skip("the plain search already walks the old bridge here")
	}
	path := findRoute(w.at, at(0, 64, 2), at(10, 64, 2), goal, 64, pathNearNodes)
	if path == nil || bridgesIn(path) != 0 {
		t.Fatalf("built %d new bridge blocks next to an existing bridge", bridgesIn(path))
	}
	// Without an old bridge it still builds one.
	w2 := testWorld{}
	w2.floor(63, -3, 2, -3, 3)
	w2.floor(63, 8, 12, -3, 3)
	if p := findRoute(w2.at, at(0, 64, 2), at(10, 64, 2), goal, 64, pathNearNodes); bridgesIn(p) == 0 {
		t.Fatal("no bridge where none exists")
	}
}

func TestResumeAt(t *testing.T) {
	route := []routeStep{{Cell: at(1, 64, 0)}, {Cell: at(2, 64, 0)}, {Cell: at(3, 64, 0), Bridge: true}, {Cell: at(4, 64, 0), Bridge: true}}
	if i := resumeAt(route, at(2, 64, 0)); i != 2 {
		t.Errorf("standing on step 1: resume at %d, want 2", i)
	}
	if i := resumeAt(route, at(3, 64, 1)); i != 2 {
		t.Errorf("knocked beside step 2: resume at %d, want 2 (walk back onto it)", i)
	}
	if i := resumeAt(route, at(3, 60, 0)); i != -1 {
		t.Errorf("fell below the route: resume at %d, want -1", i)
	}
	if i := resumeAt(route, at(9, 64, 9)); i != -1 {
		t.Errorf("far away: resume at %d, want -1", i)
	}
}
