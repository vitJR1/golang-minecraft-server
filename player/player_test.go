package player

import (
	"sync"
	"testing"
)

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

	snap := p.Snapshot()
	if snap.Y != 67 {
		t.Errorf("default spawn Y: got %v, want 67", snap.Y)
	}
	// Default spawn is the centre of the (0,0,0) block column so the
	// client renders the player standing in the middle of the cell
	// instead of clipped to a corner.
	if snap.X != 0.5 || snap.Z != 0.5 {
		t.Errorf("default spawn X/Z: got (%v, %v), want (0.5, 0.5)", snap.X, snap.Z)
	}
	if snap.Gamemode != Creative {
		t.Errorf("default gamemode: got %v, want Creative", snap.Gamemode)
	}
}

func TestGamemodeConstants(t *testing.T) {
	// Vanilla on-wire byte values — Set Gamemode + Login (Play) rely on these.
	if Survival != 0 || Creative != 1 || Adventure != 2 || Spectator != 3 {
		t.Errorf("gamemode constants drifted: S=%d C=%d A=%d Sp=%d",
			Survival, Creative, Adventure, Spectator)
	}
}

func TestMoveTo(t *testing.T) {
	p := New(1, "x", [16]byte{})
	p.MoveTo(10.5, 64, -3, true)
	snap := p.Snapshot()
	if snap.X != 10.5 || snap.Y != 64 || snap.Z != -3 {
		t.Errorf("position: got (%v,%v,%v)", snap.X, snap.Y, snap.Z)
	}
	if !snap.OnGround {
		t.Error("onGround: want true")
	}
	// Rotation unchanged.
	if snap.Yaw != 0 || snap.Pitch != 0 {
		t.Errorf("rotation should be unchanged: got (%v,%v)", snap.Yaw, snap.Pitch)
	}
}

func TestMoveAndLook(t *testing.T) {
	p := New(1, "x", [16]byte{})
	p.MoveAndLook(1, 2, 3, 45, -10, false)
	snap := p.Snapshot()
	if snap.X != 1 || snap.Y != 2 || snap.Z != 3 {
		t.Errorf("position: got (%v,%v,%v)", snap.X, snap.Y, snap.Z)
	}
	if snap.Yaw != 45 || snap.Pitch != -10 {
		t.Errorf("rotation: got (%v,%v)", snap.Yaw, snap.Pitch)
	}
	if snap.OnGround {
		t.Error("onGround: want false")
	}
}

func TestLookAt(t *testing.T) {
	p := New(1, "x", [16]byte{})
	p.MoveTo(5, 5, 5, false) // seed position so we can verify it's preserved
	p.LookAt(90, 30, true)
	snap := p.Snapshot()
	if snap.Yaw != 90 || snap.Pitch != 30 {
		t.Errorf("rotation: got (%v,%v)", snap.Yaw, snap.Pitch)
	}
	if snap.X != 5 || snap.Y != 5 || snap.Z != 5 {
		t.Errorf("position should be unchanged: got (%v,%v,%v)", snap.X, snap.Y, snap.Z)
	}
}

func TestSetGamemode(t *testing.T) {
	p := New(1, "x", [16]byte{})
	p.SetGamemode(Spectator)
	if p.Snapshot().Gamemode != Spectator {
		t.Errorf("gamemode: got %v, want Spectator", p.Snapshot().Gamemode)
	}
}

// TestConcurrent stresses the player under -race so we catch any field
// access that escapes the mutex protection.
func TestConcurrent(t *testing.T) {
	p := New(1, "x", [16]byte{})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			p.MoveAndLook(float64(i), 64, float64(-i), float32(i), 0, i%2 == 0)
		}(i)
		go func() {
			defer wg.Done()
			_ = p.Snapshot()
		}()
	}
	wg.Wait()
}
