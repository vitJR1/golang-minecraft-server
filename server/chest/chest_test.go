package chest

import (
	"testing"

	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

func TestChestsArePerPosition(t *testing.T) {
	var cs Chests
	a, b := world.Position{X: 1}, world.Position{X: 2}
	if got := cs.Contents(a); !got[0].Empty() {
		t.Fatalf("a fresh chest is empty: %+v", got[0])
	}
	cs.SetSlot(a, 0, inventory.Stack{ID: 5, Count: 3})
	cs.SetSlot(a, 99, inventory.Stack{ID: 5, Count: 3}) // out of range: ignored
	var store Store = cs.At(a)
	if got := store.Contents(); got[0].Count != 3 || store.Title() != "Chest" {
		t.Fatalf("store sees %+v (%q)", got[0], store.Title())
	}
	store.Update(func(s *Slots) { s[1] = s[0]; s[0] = inventory.Stack{} })
	if got := cs.Contents(a); !got[0].Empty() || got[1].Count != 3 {
		t.Fatalf("after update: %+v %+v", got[0], got[1])
	}
	if got := cs.Contents(b); !got[1].Empty() {
		t.Fatal("chests at other positions are separate")
	}
	if !IsEnder(world.Block{Name: "minecraft:ender_chest"}) || IsEnder(world.Block{Name: "minecraft:chest"}) {
		t.Fatal("IsEnder")
	}
}

func TestIsChestBlock(t *testing.T) {
	chests := []world.Block{world.Chest, world.EnderChest,
		{Name: "minecraft:trapped_chest"}}
	for _, b := range chests {
		if !IsChestBlock(b) {
			t.Errorf("%s should be a chest", b.Name)
		}
	}
	for _, b := range []world.Block{world.Stone, world.Air, world.RedBed} {
		if IsChestBlock(b) {
			t.Errorf("%s should not be a chest", b.Name)
		}
	}
}
