package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"minecraft-server/game"
	"minecraft-server/world"
)

func TestCreateArena(t *testing.T) {
	// A throwaway arena builder so this test doesn't depend on a game package.
	game.RegisterArenaBuilder("arenatest", func(id, name string, tmpl *world.Template, config []byte) (*game.Definition, error) {
		return &game.Definition{
			ID: id, Name: name, MinPlayers: 1, MaxPlayers: 2, Template: tmpl,
			New: func() game.Logic { return game.NoopLogic{} },
		}, nil
	})

	s := New()
	dir := t.TempDir()
	s.TemplateDir = dir
	s.RegisterTemplate("maps/test", world.NewTemplate())
	cfgPath := filepath.Join(dir, "maps", "test.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unknown template / kind / missing config all error.
	if _, err := s.CreateArena("arenatest", "missing", ""); err == nil {
		t.Error("expected unknown-template error")
	}
	if _, err := s.CreateArena("nokind", "maps/test", ""); err == nil {
		t.Error("expected unknown-kind error")
	}
	s.RegisterTemplate("maps/nocfg", world.NewTemplate())
	if _, err := s.CreateArena("arenatest", "maps/nocfg", ""); err == nil {
		t.Error("expected missing-config error")
	}

	// Explicit name registers a playable def + marks the arena.
	name, err := s.CreateArena("arenatest", "maps/test", "myarena")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { game.Unregister("myarena") })
	if name != "myarena" || !s.IsArena("myarena") {
		t.Fatalf("name=%q isArena=%v", name, s.IsArena("myarena"))
	}
	if _, ok := game.GetDef("myarena"); !ok {
		t.Error("definition not registered for arena")
	}

	// Duplicate name rejected.
	if _, err := s.CreateArena("arenatest", "maps/test", "myarena"); err == nil {
		t.Error("expected duplicate-name error")
	}

	// Auto name uses the kind prefix and is registered.
	auto, err := s.CreateArena("arenatest", "maps/test", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { game.Unregister(auto) })
	if !strings.HasPrefix(auto, "arenatest-") {
		t.Errorf("auto name %q should start with kind prefix", auto)
	}
	if !s.IsArena(auto) {
		t.Errorf("auto arena %q not tracked", auto)
	}
}

func TestNextArenaNameBedwarsPrefix(t *testing.T) {
	s := New()
	if got := s.nextArenaName("bedwars"); !strings.HasPrefix(got, "bw-") {
		t.Errorf("bedwars auto name = %q, want bw- prefix", got)
	}
}
