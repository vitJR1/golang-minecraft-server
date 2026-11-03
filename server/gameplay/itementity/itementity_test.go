package itementity

import (
	"math"
	"testing"

	"minecraft-server/server/gameplay/inventory"
)

func TestItemPayloads(t *testing.T) {
	it := &Entity{EID: 9, Stack: inventory.Stack{ID: 1, Count: 3}, X: 1, Y: 2, Z: 3}
	spawn := it.SpawnPayload()
	// eid(1) + uuid(16) + type(1) + 3 doubles(24) + 3 angles + data(1) + 3 shorts(6)
	if len(spawn) != 1+16+1+24+3+1+6 {
		t.Errorf("spawn payload length %d", len(spawn))
	}
	if spawn[17] != byte(TypeID) {
		t.Errorf("entity type byte = %d, want %d", spawn[17], TypeID)
	}
	meta := it.MetadataPayload()
	// eid, index 8, type 7, present, item id, count, TAG_End, 0xFF
	want := []byte{9, 8, 7, 1, 1, 3, 0, 0xFF}
	if string(meta) != string(want) {
		t.Errorf("metadata = % x, want % x", meta, want)
	}
	if p := PickupPayload(9, 4, 3); string(p) != string([]byte{9, 4, 3}) {
		t.Errorf("pickup payload = % x", p)
	}
}

func TestStepFallsLandsAndSlides(t *testing.T) {
	floor := func(x, y, z float64) bool { return y < 64 } // solid below y=64
	it := &Entity{X: 0.5, Y: 66, Z: 0.5, VX: 0.3}
	for i := 0; i < 100 && !(it.OnGround && it.VX == 0); i++ {
		if _, out := it.Step(floor); out {
			t.Fatal("fell out of the world")
		}
	}
	if !it.OnGround || it.Y != 64 || it.VX != 0 || it.X <= 0.5 {
		t.Fatalf("should land on the floor and slide to a stop: %+v", it)
	}
	if moved, _ := it.Step(floor); moved {
		t.Error("a resting item doesn't move")
	}
	void := &Entity{Y: -63.9, VY: -1}
	if _, out := void.Step(func(x, y, z float64) bool { return false }); !out {
		t.Error("an item below -64 is out of the world")
	}
}

// TestStepFastFallLandsOnThinFloor: near terminal speed an item moves more
// than a block per tick; it must still land on a one-block floor instead of
// skipping it.
func TestStepFastFallLandsOnThinFloor(t *testing.T) {
	floor := func(x, y, z float64) bool { return math.Floor(y) == 10 } // only y=10 is solid
	for _, start := range []float64{11.05, 11.5, 12.0, 12.3} {
		// VY -1.5 → ny ≈ start-1.54: from 11.5 that's 9.96, below the floor block.
		it := &Entity{X: 0.5, Y: start, Z: 0.5, VY: -1.5}
		for i := 0; i < 20 && !it.OnGround; i++ {
			it.Step(floor)
		}
		if !it.OnGround || it.Y != 11 {
			t.Fatalf("from y=%v: should rest on the floor at 11, got %+v", start, it)
		}
	}
}

// TestStepFallsWhenSupportBroken: a resting item falls once the block under
// it is removed, instead of hanging in the air.
func TestStepFallsWhenSupportBroken(t *testing.T) {
	broken := false
	solid := func(x, y, z float64) bool {
		b := math.Floor(y)
		return b == 60 || (b == 63 && !broken)
	}
	it := &Entity{X: 0.5, Y: 64, Z: 0.5, OnGround: true}
	if moved, _ := it.Step(solid); moved {
		t.Fatal("a supported resting item doesn't move")
	}
	broken = true
	if moved, _ := it.Step(solid); !moved || it.OnGround {
		t.Fatalf("support gone: the item should start falling, got %+v", it)
	}
	for i := 0; i < 40 && !it.OnGround; i++ {
		it.Step(solid)
	}
	if !it.OnGround || it.Y != 61 {
		t.Fatalf("should land on the next block down at 61, got %+v", it)
	}
}

func TestInPickupBox(t *testing.T) {
	for _, tc := range []struct {
		x, y, z float64
		want    bool
	}{
		{1, 64, 0, true}, {0, 66.2, 0, true}, {0, 63.6, 0, true},
		{1.3, 64, 0, false}, {0, 66.3, 0, false}, {0, 63.4, 0, false},
	} {
		if got := InPickupBox(0, 64, 0, tc.x, tc.y, tc.z); got != tc.want {
			t.Errorf("(%v,%v,%v) = %v, want %v", tc.x, tc.y, tc.z, got, tc.want)
		}
	}
}
