package server

import (
	"math"
	"testing"

	"minecraft-server/anticheat"
	"minecraft-server/world"
)

// testWorld is a map-backed blockAt for physics and pathfinding tests.
type testWorld map[world.Position]world.Block

func (w testWorld) at(p world.Position) world.Block {
	if b, ok := w[p]; ok {
		return b
	}
	return world.Air
}

// floor lays stone at y over x0..x1, z0..z1.
func (w testWorld) floor(y, x0, x1, z0, z1 int) {
	for x := x0; x <= x1; x++ {
		for z := z0; z <= z1; z++ {
			w[world.Position{X: x, Y: y, Z: z}] = world.Stone
		}
	}
}

func settle(t *testing.T, w testWorld, s *physState) {
	t.Helper()
	for i := 0; i < 200 && !s.OnGround; i++ {
		s.step(w.at, physInput{})
	}
	if !s.OnGround {
		t.Fatal("never landed")
	}
}

func TestBotFallsAndLands(t *testing.T) {
	w := testWorld{}
	w.floor(63, -5, 5, -5, 5)
	s := &physState{Pos: anticheat.Vec{X: 0.5, Y: 70, Z: 0.5}}
	settle(t, w, s)
	if s.Pos.Y != 64 {
		t.Fatalf("landed at y=%v, want 64", s.Pos.Y)
	}
}

func TestBotSprintSpeedMatchesVanilla(t *testing.T) {
	w := testWorld{}
	w.floor(63, -5, 100, -5, 5)
	s := &physState{Pos: anticheat.Vec{X: 0.5, Y: 64, Z: 0.5}, OnGround: true}
	var last float64
	for i := 0; i < 40; i++ {
		x := s.Pos.X
		s.step(w.at, physInput{DirX: 1, Sprint: true})
		last = s.Pos.X - x
	}
	if math.Abs(last-0.2806) > 0.01 {
		t.Fatalf("sprint speed %.4f blocks/tick, vanilla ≈0.2806", last)
	}
}

func TestBotJumpApex(t *testing.T) {
	w := testWorld{}
	w.floor(63, -5, 5, -5, 5)
	s := &physState{Pos: anticheat.Vec{X: 0.5, Y: 64, Z: 0.5}, OnGround: true}
	top := 0.0
	s.step(w.at, physInput{Jump: true})
	for i := 0; i < 30; i++ {
		top = math.Max(top, s.Pos.Y)
		s.step(w.at, physInput{})
	}
	if math.Abs(top-64-1.2522) > 0.01 {
		t.Fatalf("apex %.4f above ground, vanilla ≈1.2522", top-64)
	}
	if !s.OnGround || s.Pos.Y != 64 {
		t.Fatalf("didn't land back: y=%v ground=%v", s.Pos.Y, s.OnGround)
	}
}

func TestBotWallStops(t *testing.T) {
	w := testWorld{}
	w.floor(63, -5, 10, -5, 5)
	w[world.Position{X: 3, Y: 64, Z: 0}] = world.Stone
	w[world.Position{X: 3, Y: 65, Z: 0}] = world.Stone
	s := &physState{Pos: anticheat.Vec{X: 0.5, Y: 64, Z: 0.5}, OnGround: true}
	for i := 0; i < 40; i++ {
		s.step(w.at, physInput{DirX: 1, Sprint: true})
	}
	if s.Pos.X > 2.7+1e-6 || !s.Collided {
		t.Fatalf("walked into the wall: x=%v collided=%v", s.Pos.X, s.Collided)
	}
}

func TestBotStepsOntoSlabNotOntoBlock(t *testing.T) {
	slab, ok := world.BlockByName("minecraft:oak_slab")
	if !ok {
		t.Skip("oak_slab not registered")
	}
	w := testWorld{}
	w.floor(63, -5, 10, -5, 5)
	for x := 3; x <= 10; x++ { // a walkway of bottom slabs (the default state)
		w[world.Position{X: x, Y: 64, Z: 0}] = slab
	}
	s := &physState{Pos: anticheat.Vec{X: 0.5, Y: 64, Z: 0.5}, OnGround: true}
	for i := 0; i < 20; i++ {
		s.step(w.at, physInput{DirX: 1})
	}
	if s.Pos.X < 3.2 || s.Pos.Y != 64.5 {
		t.Fatalf("didn't step onto the slab: %+v", s.Pos)
	}

	w2 := testWorld{}
	w2.floor(63, -5, 10, -5, 5)
	for x := 3; x <= 10; x++ { // a one-block-high platform
		w2[world.Position{X: x, Y: 64, Z: 0}] = world.Stone
	}
	s2 := &physState{Pos: anticheat.Vec{X: 0.5, Y: 64, Z: 0.5}, OnGround: true}
	for i := 0; i < 20; i++ {
		s2.step(w2.at, physInput{DirX: 1})
	}
	if s2.Pos.Y != 64 || s2.Pos.X > 2.7+1e-6 {
		t.Fatalf("stepped onto a full block without jumping: %+v", s2.Pos)
	}
	// With a jump it climbs (then lands on the platform).
	for i := 0; i < 10; i++ {
		s2.step(w2.at, physInput{DirX: 1, Jump: true})
	}
	for i := 0; i < 20; i++ {
		s2.step(w2.at, physInput{DirX: 0.2})
	}
	if s2.Pos.Y != 65 {
		t.Fatalf("jumping didn't climb the block: %+v", s2.Pos)
	}
}

func TestBotKnockbackPush(t *testing.T) {
	w := testWorld{}
	w.floor(63, -10, 10, -5, 5)
	s := &physState{Pos: anticheat.Vec{X: 0.5, Y: 64, Z: 0.5}, OnGround: true}
	s.push(0.5, 0.4, 0)
	for i := 0; i < 30; i++ {
		s.step(w.at, physInput{})
	}
	if s.Pos.X < 1.5 || !s.OnGround {
		t.Fatalf("knockback didn't carry the bot: %+v ground=%v", s.Pos, s.OnGround)
	}
}
