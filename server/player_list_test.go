package server

import (
	"minecraft-server/player"
	"sync"
	"testing"
)

// fakeConn assembles a minimal ClientConnection with just the fields
// PlayerList touches. Connections from this helper cannot actually write;
// they're only for testing the registry/lookup methods (not Broadcast).
func fakeConn(id int32, name string) *ClientConnection {
	return &ClientConnection{
		player: &player.Player{EntityID: id, Name: name},
	}
}

func TestPlayerListAddGet(t *testing.T) {
	pl := NewPlayerList()
	c := fakeConn(1, "Alice")

	pl.Add(c)
	got, ok := pl.Get(1)
	if !ok || got != c {
		t.Errorf("Get(1): got (%v, %v), want (Alice conn, true)", got, ok)
	}
	if _, ok := pl.Get(2); ok {
		t.Error("Get(2) should be false")
	}
}

func TestPlayerListByName(t *testing.T) {
	pl := NewPlayerList()
	pl.Add(fakeConn(1, "Alice"))
	pl.Add(fakeConn(2, "Bob"))

	if c, ok := pl.ByName("Bob"); !ok || c.player.EntityID != 2 {
		t.Errorf("ByName(Bob): got %v ok=%v", c, ok)
	}
	if _, ok := pl.ByName("Charlie"); ok {
		t.Error("ByName(Charlie) should be false")
	}
}

func TestPlayerListRemove(t *testing.T) {
	pl := NewPlayerList()
	pl.Add(fakeConn(1, "Alice"))
	pl.Remove(1)
	if _, ok := pl.Get(1); ok {
		t.Error("after Remove, Get(1) should be false")
	}
	// Removing absent ID is a no-op, not an error.
	pl.Remove(999)
}

func TestPlayerListCount(t *testing.T) {
	pl := NewPlayerList()
	if pl.Count() != 0 {
		t.Errorf("empty count: %d, want 0", pl.Count())
	}
	pl.Add(fakeConn(1, "A"))
	pl.Add(fakeConn(2, "B"))
	pl.Add(fakeConn(3, "C"))
	if pl.Count() != 3 {
		t.Errorf("count after 3 adds: %d, want 3", pl.Count())
	}
	pl.Remove(2)
	if pl.Count() != 2 {
		t.Errorf("count after remove: %d, want 2", pl.Count())
	}
}

func TestPlayerListRange(t *testing.T) {
	pl := NewPlayerList()
	pl.Add(fakeConn(1, "A"))
	pl.Add(fakeConn(2, "B"))
	pl.Add(fakeConn(3, "C"))

	seen := map[int32]bool{}
	pl.Range(func(c *ClientConnection) {
		seen[c.player.EntityID] = true
	})
	if len(seen) != 3 || !seen[1] || !seen[2] || !seen[3] {
		t.Errorf("Range visited: %v", seen)
	}
}

func TestPlayerListAddOverwrites(t *testing.T) {
	pl := NewPlayerList()
	first := fakeConn(1, "Alice")
	second := fakeConn(1, "Aliceʹ") // same entity ID, different conn
	pl.Add(first)
	pl.Add(second)
	got, _ := pl.Get(1)
	if got != second {
		t.Error("second Add should overwrite the first")
	}
	if pl.Count() != 1 {
		t.Errorf("Count after overwrite: %d, want 1", pl.Count())
	}
}

func TestPlayerListConcurrent(t *testing.T) {
	// Race-detector smoke: 50 goroutines adding, 50 removing, 50 reading.
	pl := NewPlayerList()
	var wg sync.WaitGroup
	for i := int32(0); i < 50; i++ {
		wg.Add(3)
		go func(id int32) {
			defer wg.Done()
			pl.Add(fakeConn(id, "P"))
		}(i)
		go func(id int32) {
			defer wg.Done()
			pl.Remove(id)
		}(i)
		go func(id int32) {
			defer wg.Done()
			_, _ = pl.Get(id)
		}(i)
	}
	wg.Wait()
}
