package schem

import (
	"testing"

	"minecraft-server/world"
)

func TestFromWorldRoundTrip(t *testing.T) {
	w := world.NewMemoryWorld()
	stairs, _ := world.BlockByName("minecraft:oak_stairs")
	stairs.StateID = world.ResolveStateID("minecraft:oak_stairs", map[string]string{"facing": "south", "half": "top"})
	w.SetBlock(world.Position{X: 10, Y: 64, Z: -5}, world.Stone)
	w.SetBlock(world.Position{X: 12, Y: 66, Z: -3}, stairs)
	w.SetBlock(world.Position{X: 11, Y: 65, Z: -4}, world.RedBed)
	w.AddBlockEntity(world.Position{X: 11, Y: 65, Z: -4}, "minecraft:bed")
	w.SetBlock(world.Position{X: 99, Y: 64, Z: 99}, world.Stone) // outside the box

	// Corners in any order.
	s := FromWorld(w, world.Position{X: 12, Y: 66, Z: -5}, world.Position{X: 10, Y: 64, Z: -3})
	if s.Width != 3 || s.Height != 3 || s.Length != 3 {
		t.Fatalf("dims: %dx%dx%d, want 3x3x3", s.Width, s.Height, s.Length)
	}

	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("parse written schem: %v", err)
	}
	tmpl := back.ToTemplateAt(10, 64, -5)
	got := tmpl.Instantiate()
	if b := got.GetBlock(world.Position{X: 10, Y: 64, Z: -5}); b != world.Stone {
		t.Errorf("stone: got %+v", b)
	}
	if b := got.GetBlock(world.Position{X: 12, Y: 66, Z: -3}); b != stairs {
		t.Errorf("stairs with properties: got %+v, want %+v", b, stairs)
	}
	if b := got.GetBlock(world.Position{X: 11, Y: 65, Z: -4}); b != world.RedBed {
		t.Errorf("bed: got %+v", b)
	}
	if b := got.GetBlock(world.Position{X: 99, Y: 64, Z: 99}); b != world.Air {
		t.Errorf("block outside the box leaked: %+v", b)
	}
	if typ := back.BlockEntities[world.Position{X: 1, Y: 1, Z: 1}]; typ != "minecraft:bed" {
		t.Errorf("block entity: got %q, want minecraft:bed (%v)", typ, back.BlockEntities)
	}
	count := 0
	got.Range(func(world.Position, world.Block) { count++ })
	if count != 3 {
		t.Errorf("block count: %d, want 3", count)
	}
}

func TestMarshalRejectsBadDims(t *testing.T) {
	s := &Schematic{Width: 2, Height: 2, Length: 2, Blocks: make([]int32, 3)}
	if _, err := s.Marshal(); err == nil {
		t.Error("expected error for mismatched block count")
	}
}
