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

	// Explicit name: marks the arena AND spins up a running instance.
	name, err := s.CreateArena("arenatest", "maps/test", "myarena")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.RemoveInstance("myarena") })
	if name != "myarena" || !s.IsArena("myarena") {
		t.Fatalf("name=%q isArena=%v", name, s.IsArena("myarena"))
	}
	if s.GetInstance("myarena") == nil {
		t.Error("arena instance not created")
	}

	// Duplicate name rejected (instance already exists).
	if _, err := s.CreateArena("arenatest", "maps/test", "myarena"); err == nil {
		t.Error("expected duplicate-name error")
	}

	// Auto name uses the kind prefix and creates an instance.
	auto, err := s.CreateArena("arenatest", "maps/test", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.RemoveInstance(auto) })
	if !strings.HasPrefix(auto, "arenatest-") {
		t.Errorf("auto name %q should start with kind prefix", auto)
	}
	if !s.IsArena(auto) || s.GetInstance(auto) == nil {
		t.Errorf("auto arena %q not tracked/running", auto)
	}

	// Removing the instance clears arena tracking.
	_ = s.RemoveInstance("myarena")
	if s.IsArena("myarena") {
		t.Error("arena tracking should be cleared after RemoveInstance")
	}
}

func TestNextArenaNameBedwarsPrefix(t *testing.T) {
	s := New()
	if got := s.nextArenaName("bedwars"); !strings.HasPrefix(got, "bw-") {
		t.Errorf("bedwars auto name = %q, want bw- prefix", got)
	}
}
