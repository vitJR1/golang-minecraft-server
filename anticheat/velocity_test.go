package anticheat

import (
	"math"
	"testing"
)

func TestApexMatchesVanillaJump(t *testing.T) {
	if a := Apex(JumpVelocity(0)); math.Abs(a-1.2522) > 0.01 {
		t.Fatalf("jump apex %.4f, vanilla ≈1.2522", a)
	}
	if Apex(JumpVelocity(2)) <= Apex(JumpVelocity(1)) || Apex(0) != 0 {
		t.Fatal("apex must grow with the launch speed")
	}
}

func TestVelocityBudget(t *testing.T) {
	var v Velocity
	if h, up := v.Allowance(); h != 0 || up != 0 || v.Active() {
		t.Fatal("empty budget")
	}
	im := v.Add(3, 1, 4) // horizontal 5
	// Unconfirmed: counts in full on every move.
	for i := 0; i < 10; i++ {
		v.Moved(false)
	}
	if h, up := v.Allowance(); h != 5 || up != 1 {
		t.Fatalf("unconfirmed push decayed: %v %v", h, up)
	}
	v.Confirm(im)
	v.Moved(false)
	h, up := v.Allowance()
	if math.Abs(h-5*AirDrag) > 1e-9 || up != 1 {
		t.Fatalf("after one confirmed move: %v %v", h, up)
	}
	// Landing spends the launch; drag spends the rest.
	v.Moved(true)
	if _, up := v.Allowance(); up != 0 {
		t.Fatal("vertical share outlived the landing")
	}
	for i := 0; i < 200 && v.Active(); i++ {
		v.Moved(true)
	}
	if v.Active() {
		t.Fatal("a spent push never left")
	}
}

func TestHeldKeepsEndedEffect(t *testing.T) {
	level, hold := 0, 0
	if Held(2, &level, &hold, 3) != 2 {
		t.Fatal("current level not used")
	}
	for i := 0; i < 3; i++ {
		if Held(0, &level, &hold, 3) != 2 {
			t.Fatalf("ended effect dropped after %d moves", i)
		}
	}
	if Held(0, &level, &hold, 3) != 0 {
		t.Fatal("ended effect held forever")
	}
}
