package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

func registerArena(t *testing.T, s *Server, id string, players int) {
	t.Helper()
	inst := NewInstance(id, s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	s.AddInstance(inst)
	s.mu.Lock()
	s.arenas[id] = "bedwars"
	s.mu.Unlock()
	for i := 0; i < players; i++ {
		inst.Players.Add(&ClientConnection{player: player.New(int32(i+1), "P", [16]byte{})})
	}
}

func TestBedwarsArenaEntries(t *testing.T) {
	s := New()

	// No arenas → just the "create" slot.
	if e := bedwarsArenaEntries(s); len(e) != 1 || e[0].key != "create" {
		t.Fatalf("empty: %d entries, slot0=%+v", len(e), e[0])
	}

	// A populated arena is listed with its player count; an empty one isn't.
	registerArena(t, s, "bw-1", 2)
	registerArena(t, s, "bw-2", 0)

	e := bedwarsArenaEntries(s)
	if e[0].key != "create" {
		t.Errorf("slot 0 should be create, got %+v", e[0])
	}
	var foundBw1 bool
	for _, ent := range e {
		switch ent.key {
		case "bw-1":
			foundBw1 = true
			if ent.count != 2 {
				t.Errorf("bw-1 count = %d, want 2", ent.count)
			}
		case "bw-2":
			t.Error("empty arena bw-2 should not be listed")
		}
	}
	if !foundBw1 {
		t.Error("populated arena bw-1 not listed")
	}
}
