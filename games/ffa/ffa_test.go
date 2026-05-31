package ffa

import (
	"minecraft-server/game"
	"minecraft-server/world"
	"testing"
)

func TestRegistered(t *testing.T) {
	def, ok := game.GetDef("ffa")
	if !ok {
		t.Fatal("ffa not registered")
	}
	if def.MinPlayers != 2 {
		t.Errorf("MinPlayers: got %d, want 2", def.MinPlayers)
	}
	if def.MaxPlayers != 8 {
		t.Errorf("MaxPlayers: got %d, want 8", def.MaxPlayers)
	}
	if def.Template == nil {
		t.Fatal("Template is nil")
	}
}

func TestTemplateHasPlatform(t *testing.T) {
	tmpl := buildTemplate()
	if got := tmpl.BlockCount(); got != halfSize*2*halfSize*2 {
		t.Errorf("BlockCount: got %d, want %d", got, halfSize*2*halfSize*2)
	}
	w := tmpl.Instantiate()
	// Corner blocks should be stone.
	for _, p := range []world.Position{
		{X: -halfSize, Y: platformY, Z: -halfSize},
		{X: halfSize - 1, Y: platformY, Z: halfSize - 1},
		{X: 0, Y: platformY, Z: 0},
	} {
		if got := w.GetBlock(p); got != world.Stone {
			t.Errorf("(%d,%d,%d): got %+v, want Stone", p.X, p.Y, p.Z, got)
		}
	}
	// Outside the platform: air.
	if got := w.GetBlock(world.Position{X: 100, Y: platformY, Z: 100}); got != world.Air {
		t.Errorf("outside platform: got %+v, want Air", got)
	}
}

func TestSpawnsOnPlatform(t *testing.T) {
	for _, sp := range spawns {
		if sp.Y != platformY+1 {
			t.Errorf("spawn Y: got %d, want %d (1 above platform top)", sp.Y, platformY+1)
		}
		if sp.X < -halfSize || sp.X >= halfSize {
			t.Errorf("spawn X out of platform: got %d", sp.X)
		}
		if sp.Z < -halfSize || sp.Z >= halfSize {
			t.Errorf("spawn Z out of platform: got %d", sp.Z)
		}
	}
}
