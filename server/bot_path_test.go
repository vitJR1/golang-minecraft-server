package server

import (
	"math"
	"testing"

	"minecraft-server/world"
)

func at(x, y, z int) world.Position { return world.Position{X: x, Y: y, Z: z} }

// reach is a goal: exactly that cell.
func reach(p world.Position) func(world.Position) bool {
	return func(c world.Position) bool { return c == p }
}

// blocks is a near search that may place n blocks and never leaps.
func blocks(n int) routeOpts { return routeOpts{maxBlocks: n, budget: pathNearNodes} }

func bridgesIn(path []routeStep) int {
	n := 0
	for _, s := range path {
		n += len(s.Place)
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
	path := findPath(w.at, at(0, 64, 0), at(6, 64, 0), reach(at(6, 64, 0)), blocks(0))
	if path == nil {
		t.Fatal("no way round the wall")
	}
	for _, s := range path {
		if s.Cell.X == 3 && s.Cell.Z >= -3 && s.Cell.Z <= 3 {
			t.Fatalf("path goes through the wall at %v", s.Cell)
		}
		if len(s.Place) > 0 {
			t.Fatal("built when walking round was possible")
		}
	}
}

func TestPathBridgesAGap(t *testing.T) {
	w := testWorld{}
	w.floor(63, -3, 2, -2, 2) // island A
	w.floor(63, 8, 12, -2, 2) // island B, a 5-block gap at x=3..7
	path := findPath(w.at, at(0, 64, 0), at(10, 64, 0), reach(at(10, 64, 0)), blocks(64))
	if path == nil {
		t.Fatal("no bridge across the gap")
	}
	if n := bridgesIn(path); n != 5 {
		t.Fatalf("%d bridge steps, want 5 (the gap)", n)
	}
	if findPath(w.at, at(0, 64, 0), at(10, 64, 0), reach(at(10, 64, 0)), blocks(3)) != nil {
		t.Fatal("3 blocks were enough for a 5-block gap")
	}
	if findPath(w.at, at(0, 64, 0), at(10, 64, 0), reach(at(10, 64, 0)), blocks(0)) != nil {
		t.Fatal("crossed the void without blocks")
	}
}

func TestPathJumpsUpAndDropsDown(t *testing.T) {
	w := testWorld{}
	w.floor(63, -2, 10, -2, 2)
	w.floor(64, 3, 5, -2, 2) // a one-block step up at x=3..5
	path := findPath(w.at, at(0, 64, 0), at(8, 64, 0), reach(at(8, 64, 0)), blocks(0))
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
	if p := findPath(tall.at, at(0, 64, 0), at(8, 64, 0), reach(at(8, 64, 0)), blocks(0)); p != nil {
		t.Fatalf("jumped a two-block wall: %v", p)
	}
}

func TestPathDropsFromALedge(t *testing.T) {
	w := testWorld{}
	w.floor(66, -2, 2, -2, 2) // a ledge 3 blocks above the ground
	w.floor(63, 3, 8, -2, 2)
	path := findPath(w.at, at(0, 67, 0), at(6, 64, 0), reach(at(6, 64, 0)), blocks(0))
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
	path := findPath(w.at, at(0, 64, 0), at(6, 64, 0), reach(at(6, 64, 0)), blocks(0))
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
	greedy := findPath(w.at, at(0, 64, 2), at(10, 64, 2), goal, blocks(64))
	if bridgesIn(greedy) == 0 {
		t.Skip("the plain search already walks the old bridge here")
	}
	path := findRoute(w.at, at(0, 64, 2), at(10, 64, 2), goal, blocks(64))
	if path == nil || bridgesIn(path) != 0 {
		t.Fatalf("built %d new bridge blocks next to an existing bridge", bridgesIn(path))
	}
	// Without an old bridge it still builds one.
	w2 := testWorld{}
	w2.floor(63, -3, 2, -3, 3)
	w2.floor(63, 8, 12, -3, 3)
	if p := findRoute(w2.at, at(0, 64, 2), at(10, 64, 2), goal, blocks(64)); bridgesIn(p) == 0 {
		t.Fatal("no bridge where none exists")
	}
}

func TestResumeAt(t *testing.T) {
	route := []routeStep{{Cell: at(1, 64, 0)}, {Cell: at(2, 64, 0)}, {Cell: at(3, 64, 0), Place: []world.Position{at(3, 63, 0)}}, {Cell: at(4, 64, 0), Place: []world.Position{at(4, 63, 0)}}}
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

// TestPathBuildsStairs: a three-block wall with nothing to step on is
// climbed by stair steps — a block in front, a jump onto it — when the
// search may place blocks.
func TestPathBuildsStairs(t *testing.T) {
	w := testWorld{}
	w.floor(63, -2, 3, -1, 1)
	w.floor(66, 4, 8, -1, 1) // a platform three blocks up, against a wall
	for y := 64; y <= 66; y++ {
		for z := -1; z <= 1; z++ {
			w[at(4, y, z)] = world.Stone
		}
	}
	goal := reach(at(6, 67, 0))
	if p := findPath(w.at, at(0, 64, 0), at(6, 67, 0), goal, blocks(0)); p != nil {
		t.Fatalf("climbed a 3-block wall without blocks: %v", p)
	}
	path := findPath(w.at, at(0, 64, 0), at(6, 67, 0), goal, blocks(8))
	if path == nil {
		t.Fatal("no stairs up the wall")
	}
	stairs := 0
	for i, s := range path {
		prevY := 64
		if i > 0 {
			prevY = path[i-1].Cell.Y
		}
		if len(s.Place) > 0 && s.Cell.Y == prevY+1 {
			stairs++
		}
	}
	if stairs < 2 {
		t.Fatalf("%d stair steps in %v, want ≥2", stairs, path)
	}
}

// TestPathLeapsAGap: a two-block gap is leapt when leaps are allowed, and
// needs blocks otherwise.
func TestPathLeapsAGap(t *testing.T) {
	w := testWorld{}
	w.floor(63, -3, 2, -1, 1)
	w.floor(63, 5, 9, -1, 1) // gap at x=3,4
	goal := reach(at(7, 64, 0))
	if p := findPath(w.at, at(0, 64, 0), at(7, 64, 0), goal, blocks(0)); p != nil {
		t.Fatalf("crossed without leaping or blocks: %v", p)
	}
	opts := blocks(0)
	opts.maxLeap = 2
	path := findPath(w.at, at(0, 64, 0), at(7, 64, 0), goal, opts)
	leaps := 0
	for _, s := range path {
		if s.Leap {
			leaps++
		}
	}
	if path == nil || leaps != 1 || bridgesIn(path) != 0 {
		t.Fatalf("want one leap and no blocks: %v", path)
	}
	opts.maxLeap = 1
	if p := findPath(w.at, at(0, 64, 0), at(7, 64, 0), goal, opts); p != nil {
		t.Fatalf("leapt a 2-gap with maxLeap 1: %v", p)
	}
}

// TestPathSlabHeights: a bottom slab on a block is 1.5 up — beyond a jump —
// while a slab on the floor is a half step, and from it a full block is
// another half step.
func TestPathSlabHeights(t *testing.T) {
	slab, ok := world.BlockByName("minecraft:oak_slab") // bottom by default
	if !ok {
		t.Skip("oak_slab not registered")
	}
	// A block with a slab on it, the only way on: no path.
	w := testWorld{}
	w.floor(63, -2, 2, -1, 1)
	w.floor(64, 3, 6, -1, 1)
	w.floor(65, 3, 6, -1, 1) // slabs on the blocks
	for x := 3; x <= 6; x++ {
		for z := -1; z <= 1; z++ {
			w[at(x, 65, z)] = slab
		}
	}
	if p := findPath(w.at, at(0, 64, 0), at(5, 65, 0), reach(at(5, 65, 0)), blocks(0)); p != nil {
		t.Fatalf("jumped 1.5 blocks onto a slab on a block: %v", p)
	}

	// Floor → slab → block: two half steps.
	w2 := testWorld{}
	w2.floor(63, -2, 8, -1, 1)
	for z := -1; z <= 1; z++ {
		w2[at(3, 64, z)] = slab
		for x := 4; x <= 8; x++ {
			w2[at(x, 64, z)] = world.Stone
		}
	}
	path := findPath(w2.at, at(0, 64, 0), at(6, 65, 0), reach(at(6, 65, 0)), blocks(0))
	if path == nil {
		t.Fatal("no way up the slab step")
	}
	onSlab := false
	for _, s := range path {
		if s.Cell == at(3, 64, 0) {
			onSlab = true
		}
	}
	if !onSlab {
		t.Fatalf("path skipped the slab: %v", path)
	}
}

// TestBotClimbsASlabStep: the bot walks the floor → slab → block steps
// for real (physics).
func TestBotClimbsASlabStep(t *testing.T) {
	slab, ok := world.BlockByName("minecraft:oak_slab")
	if !ok {
		t.Skip("oak_slab not registered")
	}
	w := testWorld{}
	w.floor(63, -2, 10, -1, 1)
	for z := -1; z <= 1; z++ {
		w[at(3, 64, z)] = slab
		for x := 4; x <= 10; x++ {
			w[at(x, 64, z)] = world.Stone
		}
	}
	path := findPath(w.at, at(0, 64, 0), at(8, 65, 0), reach(at(8, 65, 0)), blocks(0))
	if path == nil {
		t.Fatal("no path")
	}
	var s physState
	s.Pos.X, s.Pos.Y, s.Pos.Z = 0.5, 64, 0.5
	idx := 0
	for tick := 0; tick < 200 && idx < len(path); tick++ {
		cell := path[idx].Cell
		dx, dz := float64(cell.X)+0.5-s.Pos.X, float64(cell.Z)+0.5-s.Pos.Z
		d := math.Hypot(dx, dz)
		in := physInput{}
		if d > 1e-6 {
			in.DirX, in.DirZ = dx/d, dz/d
		}
		here := feetCell(s.Pos.X, s.Pos.Y, s.Pos.Z)
		if cell.Y > here.Y && s.OnGround && d < 1.5 {
			in.Jump = true
		}
		s.step(w.at, in)
		if d < 0.3 && math.Abs(s.Pos.Y-standY(w.at, cell)) < 0.1 {
			idx++
		}
	}
	if idx < len(path) {
		t.Fatalf("stuck at step %d/%d, pos %+v", idx, len(path), s.Pos)
	}
}
