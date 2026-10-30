package server

import (
	"testing"

	"minecraft-server/world"
)

func TestHologramSpawnUpdateRemove(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	h := inst.SpawnHologram(0.5, 72, 0.5, "hello")
	if h == nil {
		t.Fatal("SpawnHologram returned nil")
	}
	if len(inst.holograms) != 1 || h.Text() != "hello" {
		t.Fatalf("hologram not registered: %d, text %q", len(inst.holograms), h.Text())
	}
	h.SetText("world")
	if h.Text() != "world" {
		t.Errorf("SetText: %q", h.Text())
	}
	h.Remove()
	h.Remove() // idempotent
	if len(inst.holograms) != 0 {
		t.Errorf("hologram still registered after Remove: %d", len(inst.holograms))
	}
}
