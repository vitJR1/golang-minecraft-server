package anticheat

import (
	"math"
	"testing"

	"minecraft-server/world"
)

// blocks is a map-backed BlockAt with a stone floor at y=63.
func blocks(extra map[world.Position]world.Block) BlockAt {
	return func(p world.Position) world.Block {
		if b, ok := extra[p]; ok {
			return b
		}
		if p.Y == 63 {
			return world.Stone
		}
		return world.Air
	}
}

func TestTerrain(t *testing.T) {
	wall := world.Position{X: 2, Y: 64, Z: 0}
	at := blocks(map[world.Position]world.Block{
		wall:                 world.Stone,
		{X: 2, Y: 65, Z: 0}:  world.Stone,
		{X: 10, Y: 64, Z: 0}: world.Water,
	})
	if !OnGround(at, Vec{X: 0.5, Y: 64, Z: 0.5}) || OnGround(at, Vec{X: 0.5, Y: 65, Z: 0.5}) {
		t.Error("OnGround")
	}
	if !Phased(at, Vec{X: 1.5, Y: 64, Z: 0.5}, Vec{X: 3.5, Y: 64, Z: 0.5}, false) {
		t.Error("walking through the wall phases")
	}
	if Phased(at, Vec{X: 0.5, Y: 64, Z: 0.5}, Vec{X: 1.6, Y: 64, Z: 0.5}, false) {
		t.Error("walking up to the wall doesn't")
	}
	if Phased(at, Vec{X: 2.5, Y: 64, Z: 0.5}, Vec{X: 2.6, Y: 64, Z: 0.5}, false) {
		t.Error("already inside a wall: left alone")
	}
	if !MotionExempt(at, Vec{X: 10.5, Y: 64, Z: 0.5}, false) || MotionExempt(at, Vec{X: 0.5, Y: 64, Z: 0.5}, false) {
		t.Error("MotionExempt")
	}
	if n := len(BlocksUnder(at, Vec{X: 0.5, Y: 64, Z: 0.5})); n == 0 {
		t.Error("BlocksUnder sees the floor")
	}
}

func TestJudgeSpeedAndFlight(t *testing.T) {
	var m MoveState
	m.Landed(64)
	from := Vec{X: 0, Y: 64, Z: 0}
	// One big step is buffered; repeated ones flag.
	var check string
	for i := 0; i < 5 && check == ""; i++ {
		check, _ = m.Judge(Motion{From: from, To: Vec{X: 1.0, Y: 64}, Ground: true})
	}
	if check != "movement.speed" {
		t.Fatalf("speed: %q", check)
	}
	m = MoveState{}
	m.Landed(64)
	if check, _ := m.Judge(Motion{From: from, To: Vec{Y: 64 + 1.2}}); check != "" {
		t.Errorf("a jump's height is fine: %q", check)
	}
	if check, _ := m.Judge(Motion{From: Vec{Y: 65.2}, To: Vec{Y: 66.5}}); check != "movement.fly" {
		t.Errorf("rising far above the last ground flies: %q", check)
	}
	m = MoveState{}
	m.Landed(64)
	check = ""
	for i := 0; i < 40 && check == ""; i++ {
		check, _ = m.Judge(Motion{From: Vec{Y: 65}, To: Vec{Y: 65}})
	}
	if check != "movement.fly" {
		t.Errorf("hovering: %q", check)
	}
}

func TestKnockFollow(t *testing.T) {
	if _, ok := StartKnock(&Impulse{H: 0.1, Up: 0.1}, Vec{}); ok {
		t.Error("a tiny push isn't judged")
	}
	k, ok := StartKnock(&Impulse{DirX: 1, H: 0.8, Up: 0.4}, Vec{Y: 64})
	if !ok {
		t.Fatal("push not started")
	}
	open := func(Vec) bool { return false }
	var ev string
	for i := 0; i < knockMoves; i++ {
		ev = k.Follow(Vec{Y: 64}, false, open) // never moves
	}
	if ev == "" || k.Active {
		t.Errorf("anti-knockback: %q", ev)
	}
	k, _ = StartKnock(&Impulse{DirX: 1, H: 0.8}, Vec{Y: 64})
	for i := 0; i < knockMoves; i++ {
		ev = k.Follow(Vec{Y: 64}, false, func(Vec) bool { return true }) // a wall
	}
	if ev != "" {
		t.Errorf("a wall excuses it: %q", ev)
	}
}

func TestSanity(t *testing.T) {
	if oob, fast := MoveSane(Vec{}, Vec{X: 5}); oob || fast {
		t.Error("a normal move")
	}
	if _, fast := MoveSane(Vec{}, Vec{X: 11}); !fast {
		t.Error("11 blocks in a packet")
	}
	if oob, _ := MoveSane(Vec{X: 3e7}, Vec{X: 3e7 + 1}); !oob {
		t.Error("past the world border")
	}
	if Finite64(1, math.NaN()) || !Finite64(1, 2) || Finite32(float32(math.Inf(1))) {
		t.Error("Finite")
	}
	if !CursorValid(0, 0.5, 1) || CursorValid(1.5, 0, 0) {
		t.Error("CursorValid")
	}
	if b := EntityBox("minecraft:item_frame", 0, 0, 0); b.Max.X-b.Min.X != 0.75 {
		t.Errorf("item frame box %+v", b)
	}
	eye := Vec{Y: 1.62}
	if ok, _ := DigAimed([]Vec{eye}, []Vec{{Z: 1}}, Vec{Z: 4, Y: 1.62}); !ok {
		t.Error("digging straight ahead")
	}
	if ok, _ := DigAimed([]Vec{eye}, []Vec{{Z: 1}}, Vec{Z: -4, Y: 1.62}); ok {
		t.Error("digging behind the back")
	}
}
