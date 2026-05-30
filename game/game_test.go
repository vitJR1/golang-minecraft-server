package game

import (
	"minecraft-server/world"
	"testing"
)

// Compile-time assertion that NoopLogic satisfies Logic.
var _ Logic = NoopLogic{}

// embeddedLogic checks that embedding NoopLogic + overriding one method
// still satisfies the interface — the common plugin pattern.
type embeddedLogic struct {
	NoopLogic
	joins int
}

func (e *embeddedLogic) OnPlayerJoin(_ *Ctx, _ PlayerHandle) { e.joins++ }

var _ Logic = (*embeddedLogic)(nil)

func TestNoopLogicReturnsAllowing(t *testing.T) {
	var n NoopLogic
	if !n.OnBlockBreak(nil, nil, world.Position{}) {
		t.Error("OnBlockBreak default should be true (allow)")
	}
	if !n.OnBlockPlace(nil, nil, world.Position{}, world.Stone) {
		t.Error("OnBlockPlace default should be true (allow)")
	}
	got, ok := n.OnChat(nil, nil, "hello")
	if !ok || got != "hello" {
		t.Errorf("OnChat default: got (%q, %v), want (%q, true)", got, ok, "hello")
	}
}

func TestRegisterAndGet(t *testing.T) {
	t.Cleanup(reset)
	def := &Definition{
		ID:         "test-game",
		Name:       "Test",
		MinPlayers: 1,
		MaxPlayers: 2,
		Template:   world.NewTemplate(),
		New:        func() Logic { return NoopLogic{} },
	}
	Register(def)

	got, ok := GetDef("test-game")
	if !ok {
		t.Fatal("GetDef returned ok=false after Register")
	}
	if got != def {
		t.Error("GetDef returned different *Definition")
	}
}

func TestAllReturnsAllRegistered(t *testing.T) {
	t.Cleanup(reset)
	Register(&Definition{
		ID:       "a",
		Template: world.NewTemplate(),
		New:      func() Logic { return NoopLogic{} },
	})
	Register(&Definition{
		ID:       "b",
		Template: world.NewTemplate(),
		New:      func() Logic { return NoopLogic{} },
	})
	defs := All()
	if len(defs) != 2 {
		t.Errorf("All: got %d defs, want 2", len(defs))
	}
}

func TestRegisterPanicsOnDuplicateID(t *testing.T) {
	t.Cleanup(reset)
	Register(&Definition{
		ID:       "dup",
		Template: world.NewTemplate(),
		New:      func() Logic { return NoopLogic{} },
	})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate Register")
		}
	}()
	Register(&Definition{
		ID:       "dup",
		Template: world.NewTemplate(),
		New:      func() Logic { return NoopLogic{} },
	})
}

func TestRegisterPanicsOnNilFields(t *testing.T) {
	cases := []struct {
		name string
		def  *Definition
	}{
		{"nil definition", nil},
		{"empty ID", &Definition{ID: "", New: func() Logic { return NoopLogic{} }}},
		{"nil New", &Definition{ID: "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Error("expected panic")
				}
			}()
			Register(tc.def)
		})
	}
}

func TestGetDefMissing(t *testing.T) {
	t.Cleanup(reset)
	if _, ok := GetDef("never-registered"); ok {
		t.Error("GetDef should return ok=false for unknown ID")
	}
}
