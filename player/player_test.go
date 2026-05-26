package player

import "testing"

func TestNewSetsIdentityAndDefaults(t *testing.T) {
	uuid := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	p := New(42, "Alice", uuid)

	if p.EntityID != 42 {
		t.Errorf("EntityID: got %d, want 42", p.EntityID)
	}
	if p.Name != "Alice" {
		t.Errorf("Name: got %q, want %q", p.Name, "Alice")
	}
	if p.UUID != uuid {
		t.Errorf("UUID mismatch: got %x", p.UUID)
	}
	if p.Y != 80 {
		t.Errorf("default spawn Y: got %v, want 80", p.Y)
	}
	if p.X != 0 || p.Z != 0 {
		t.Errorf("default spawn X/Z: got (%v, %v), want (0, 0)", p.X, p.Z)
	}
	if p.Gamemode != Creative {
		t.Errorf("default gamemode: got %v, want Creative", p.Gamemode)
	}
}

func TestGamemodeConstants(t *testing.T) {
	// Vanilla on-wire byte values — Set Gamemode + Login (Play) rely on these.
	if Survival != 0 || Creative != 1 || Adventure != 2 || Spectator != 3 {
		t.Errorf("gamemode constants drifted: S=%d C=%d A=%d Sp=%d",
			Survival, Creative, Adventure, Spectator)
	}
}
