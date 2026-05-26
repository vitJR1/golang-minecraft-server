package world

import (
	"sync"
	"testing"
)

func TestMemoryWorldEmptyIsAir(t *testing.T) {
	w := NewMemoryWorld()
	got := w.GetBlock(Position{1, 2, 3})
	if got != Air {
		t.Errorf("unset position should be Air, got %+v", got)
	}
}

func TestMemoryWorldGetSet(t *testing.T) {
	w := NewMemoryWorld()
	p := Position{10, 64, -5}
	w.SetBlock(p, Stone)
	if got := w.GetBlock(p); got != Stone {
		t.Errorf("got %+v, want Stone", got)
	}
}

func TestMemoryWorldSetAirDeletes(t *testing.T) {
	w := NewMemoryWorld()
	p := Position{0, 0, 0}
	w.SetBlock(p, Stone)
	w.SetBlock(p, Air)
	if got := w.GetBlock(p); got != Air {
		t.Errorf("after Set(Air), got %+v, want Air", got)
	}
	// The internal map should be empty — testing implementation detail, but
	// the "sparse" property matters for memory use under repeated edits.
	if len(w.blocks) != 0 {
		t.Errorf("after Set(Air), internal map size = %d, want 0", len(w.blocks))
	}
}

func TestMemoryWorldOverwrites(t *testing.T) {
	w := NewMemoryWorld()
	p := Position{5, 5, 5}
	w.SetBlock(p, Stone)
	w.SetBlock(p, Dirt)
	if got := w.GetBlock(p); got != Dirt {
		t.Errorf("overwrite failed: got %+v, want Dirt", got)
	}
}

func TestMemoryWorldDistinctPositions(t *testing.T) {
	w := NewMemoryWorld()
	w.SetBlock(Position{0, 0, 0}, Stone)
	w.SetBlock(Position{0, 0, 1}, Dirt)
	if w.GetBlock(Position{0, 0, 0}) != Stone {
		t.Error("position {0,0,0} corrupted")
	}
	if w.GetBlock(Position{0, 0, 1}) != Dirt {
		t.Error("position {0,0,1} corrupted")
	}
}

func TestMemoryWorldConcurrentReadsAndWrites(t *testing.T) {
	// Run racy traffic; the test is here to make `go test -race` catch
	// regressions if anyone removes the mutex.
	w := NewMemoryWorld()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			w.SetBlock(Position{i, 0, 0}, Stone)
		}(i)
		go func(i int) {
			defer wg.Done()
			_ = w.GetBlock(Position{i, 0, 0})
		}(i)
	}
	wg.Wait()
}

func TestBlocksHaveDistinctStateIDs(t *testing.T) {
	// Cheap sanity: two distinct constants should not share a state ID.
	all := []Block{Air, Stone, GrassBlock, Dirt, Cobblestone, Bedrock}
	seen := map[int32]Block{}
	for _, b := range all {
		if prev, ok := seen[b.StateID]; ok {
			t.Errorf("StateID %d shared by %q and %q", b.StateID, prev.Name, b.Name)
		}
		seen[b.StateID] = b
	}
}
