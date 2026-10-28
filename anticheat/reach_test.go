package anticheat

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestBoxDistance(t *testing.T) {
	b := FeetBox(Vec{0, 64, 0}, 0.6, 1.8) // x,z ∈ [-0.3, 0.3], y ∈ [64, 65.8]
	if d := b.DistanceTo(Vec{0, 65, 0}); d != 0 {
		t.Fatalf("inside: %v", d)
	}
	if d := b.DistanceTo(Vec{3.3, 65, 0}); !near(d, 3) {
		t.Fatalf("side: %v, want 3", d)
	}
	if d := b.DistanceTo(Vec{0, 67.8, 0}); !near(d, 2) {
		t.Fatalf("above: %v, want 2", d)
	}
	if d := b.Expand(0.1).DistanceTo(Vec{3.3, 65, 0}); !near(d, 2.9) {
		t.Fatalf("expanded: %v, want 2.9", d)
	}
}

func TestEye(t *testing.T) {
	if e := Eye(Vec{1, 64, 2}, false); e.Y != 64+EyeHeight {
		t.Fatalf("standing eye %v", e)
	}
	if e := Eye(Vec{1, 64, 2}, true); e.Y != 64+SneakEyeHeight {
		t.Fatalf("sneaking eye %v", e)
	}
}

func TestMinReachTakesClosestSample(t *testing.T) {
	eye := Vec{0, 65.62, 0}
	samples := []Sample{
		{Tick: 1, X: 5, Y: 64, Z: 0},
		{Tick: 2, X: 3, Y: 64, Z: 0}, // box face at 2.7, margin → 2.6
		{Tick: 3, X: 8, Y: 64, Z: 0},
	}
	d, ok := MinReach(eye, samples)
	if !ok || !near(d, 2.6) {
		t.Fatalf("min reach = %v %v, want 2.6", d, ok)
	}
	if _, ok := MinReach(eye, nil); ok {
		t.Fatal("no samples reported ok")
	}
	// A sneaking victim's box is shorter: from above it is farther.
	high := Vec{0, 68, 0}
	stand, _ := MinReach(high, []Sample{{X: 0, Y: 64, Z: 0}})
	crouch, _ := MinReach(high, []Sample{{X: 0, Y: 64, Z: 0, Sneaking: true}})
	if !(crouch > stand) {
		t.Fatalf("sneak box not shorter: stand %v crouch %v", stand, crouch)
	}
}

func TestSeenFrom(t *testing.T) {
	if got := SeenFrom(100, 90, true); got != 90-InterpolationTicks {
		t.Fatalf("confirmed: %d", got)
	}
	if got := SeenFrom(100, 0, false); got != 100-UnconfirmedWindow {
		t.Fatalf("unconfirmed: %d", got)
	}
	if got := SeenFrom(2, 1, true); got != 0 {
		t.Fatalf("underflow: %d", got)
	}
	if got := SeenFrom(100, 500, true); got != 100-UnconfirmedWindow {
		t.Fatalf("confirmed tick from the future (old epoch) must fall back: %d", got)
	}
}

func TestLookVector(t *testing.T) {
	cases := []struct {
		yaw, pitch float32
		want       Vec
	}{
		{0, 0, Vec{0, 0, 1}},   // south, +Z
		{90, 0, Vec{-1, 0, 0}}, // west, −X
		{180, 0, Vec{0, 0, -1}},
		{0, 90, Vec{0, -1, 0}}, // straight down
	}
	for _, tc := range cases {
		got := Look(tc.yaw, tc.pitch)
		if Dist(got, tc.want) > 1e-9 {
			t.Errorf("Look(%v, %v) = %+v, want %+v", tc.yaw, tc.pitch, got, tc.want)
		}
	}
}

func TestRayAndAngle(t *testing.T) {
	box := FeetBox(Vec{0, 64, 3}, 0.6, 1.8)
	eye := Vec{0, 65.62, 0}
	if !RayHitsBox(eye, Vec{0, 0, 1}, box, 6) {
		t.Fatal("straight-ahead ray missed")
	}
	if RayHitsBox(eye, Vec{0, 0, -1}, box, 6) {
		t.Fatal("ray behind hit")
	}
	if RayHitsBox(eye, Vec{0, 0, 1}, box, 2) {
		t.Fatal("ray hit past its length")
	}
	if a := AngleTo(eye, Vec{1, 0, 0}, Vec{0, 65.62, 5}); math.Abs(a-90) > 1e-6 {
		t.Fatalf("angle %v, want 90", a)
	}
}

func TestFaceVisible(t *testing.T) {
	// Block at (2, 63, 0); eye above it and west of it.
	eye := Vec{0.5, 65.62, 0.5}
	if !FaceVisible(eye, 2, 63, 0, 1, 0.1) { // top
		t.Error("top face hidden from above")
	}
	if !FaceVisible(eye, 2, 63, 0, 4, 0.1) { // west side faces the eye
		t.Error("west face hidden from the west")
	}
	if FaceVisible(eye, 2, 63, 0, 5, 0.1) { // east side faces away
		t.Error("east face visible from the west")
	}
	if FaceVisible(eye, 2, 63, 0, 0, 0.1) { // bottom
		t.Error("bottom face visible from above")
	}
}
