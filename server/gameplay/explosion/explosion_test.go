package explosion

import (
	"testing"

	"minecraft-server/world"
)

func TestExplosionRecordsAreRelativeOffsets(t *testing.T) {
	recs := Records(0.5, 64.06, 0.5, []world.Position{{X: 1, Y: 64, Z: 0}, {X: -3, Y: 63, Z: 2}})
	want := []Record{{1, 0, 0}, {-3, -1, 2}}
	if len(recs) != len(want) {
		t.Fatalf("records = %v", recs)
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Errorf("record %d = %v, want %v", i, recs[i], want[i])
		}
	}
}
