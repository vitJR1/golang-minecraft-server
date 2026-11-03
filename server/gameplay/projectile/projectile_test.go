package projectile

import "testing"

func TestThrowableEntityID(t *testing.T) {
	cases := map[string]int32{
		"minecraft:egg":         EggEntityID,
		"minecraft:snowball":    SnowballEntityID,
		"minecraft:ender_pearl": EnderPearlEntityID,
	}
	for item, want := range cases {
		if id, ok := ThrowableEntityID(item); !ok || id != want {
			t.Errorf("%s: got %d,%v want %d", item, id, ok, want)
		}
	}
	if _, ok := ThrowableEntityID("minecraft:diamond"); ok {
		t.Error("diamond should not be throwable")
	}
}

func TestDrawForceCurve(t *testing.T) {
	if f := DrawForce(5); f < 0.18 || f > 0.19 { // t=0.25 → (0.0625+0.5)/3
		t.Errorf("5 ticks: %v", f)
	}
	if f := DrawForce(20); f != 1 {
		t.Errorf("20 ticks: %v, want 1 (full)", f)
	}
	if f := DrawForce(100); f != 1 {
		t.Errorf("overdraw must cap at 1, got %v", f)
	}
	if ArrowDamage(3, 0, false) != 6 || ArrowDamage(3, 1, false) != 9 || ArrowDamage(3, 0, true) != 9 {
		t.Errorf("damage: %v %v %v", ArrowDamage(3, 0, false), ArrowDamage(3, 1, false), ArrowDamage(3, 0, true))
	}
}

func TestFlightAdvanceAndExpire(t *testing.T) {
	f := Flight{VX: 1, MaxTicks: 2}
	nx, ny, _ := f.Advance()
	if nx != 0.99 || ny >= 0 || f.Ticks != 1 || f.Expired() {
		t.Fatalf("first tick: %v %v %+v", nx, ny, f)
	}
	straight := Flight{VX: 1, NoGravity: true}
	if _, ny, _ := straight.Advance(); ny != 0 {
		t.Errorf("a no-gravity flight stays level: %v", ny)
	}
	f.Advance()
	if !f.Expired() {
		t.Error("expired after MaxTicks")
	}
}

func TestMarchStopsBeforeTheWall(t *testing.T) {
	wall := func(x, y, z float64) bool { return x >= 2 }
	x, _, _, hit := March(wall, 0, 0, 0, 3, 0, 0)
	if !hit || x >= 2 || x < 1.85 {
		t.Fatalf("march: x=%v hit=%v", x, hit)
	}
	if _, _, _, hit := March(wall, 0, 0, 0, 1.5, 0, 0); hit {
		t.Error("nothing in the way")
	}
}
