package server

import (
	"strings"
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

func registerArena(t *testing.T, s *Server, id string, players int) {
	t.Helper()
	registerArenaOfKind(t, s, id, bedwarsKindFull, players)
}

func registerArenaOfKind(t *testing.T, s *Server, id, kind string, players int) {
	t.Helper()
	inst := NewInstance(id, s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	s.AddInstance(inst)
	s.mu.Lock()
	s.arenas[id] = kind
	s.mu.Unlock()
	for i := 0; i < players; i++ {
		inst.Players.Add(&ClientConnection{player: player.New(int32(i+1), "P", [16]byte{})})
	}
}

func TestBedwarsArenaEntries(t *testing.T) {
	s := New()

	// No arenas → just the per-mode "create" buttons (4×4, then 1×1 duel).
	if e := bedwarsArenaEntries(s); len(e) != 2 ||
		e[0].key != "create:"+bedwarsKindFull || e[1].key != "create:"+bedwarsKindDuel {
		t.Fatalf("empty: %d entries, slot0=%+v slot1=%+v", len(e), e[0], e[1])
	}

	// A populated arena is listed with its player count; an empty one isn't.
	// Duel arenas are listed too, labelled as duels.
	registerArena(t, s, "bw-1", 2)
	registerArena(t, s, "bw-2", 0)
	registerArenaOfKind(t, s, "bw-1x1-1", bedwarsKindDuel, 1)

	e := bedwarsArenaEntries(s)
	if e[0].key != "create:"+bedwarsKindFull || e[1].key != "create:"+bedwarsKindDuel {
		t.Errorf("create buttons should stay first, got %+v / %+v", e[0], e[1])
	}
	var foundBw1, foundDuel bool
	for _, ent := range e {
		switch ent.key {
		case "bw-1":
			foundBw1 = true
			if ent.count != 2 {
				t.Errorf("bw-1 count = %d, want 2", ent.count)
			}
		case "bw-2":
			t.Error("empty arena bw-2 should not be listed")
		case "bw-1x1-1":
			foundDuel = true
			if ent.count != 1 || !strings.Contains(ent.name, "Duel") {
				t.Errorf("duel entry = %+v, want count 1 and a Duel label", ent)
			}
		}
	}
	if !foundBw1 {
		t.Error("populated arena bw-1 not listed")
	}
	if !foundDuel {
		t.Error("populated duel arena bw-1x1-1 not listed")
	}
}

func TestNextArenaNameBedwarsKinds(t *testing.T) {
	s := New()
	if n := s.nextArenaName(bedwarsKindFull); !strings.HasPrefix(n, "bw-") || strings.HasPrefix(n, "bw-1x1-") {
		t.Errorf("full kind name = %q, want bw-<n>", n)
	}
	if n := s.nextArenaName(bedwarsKindDuel); !strings.HasPrefix(n, "bw-1x1-") {
		t.Errorf("duel kind name = %q, want bw-1x1-<n>", n)
	}
}
