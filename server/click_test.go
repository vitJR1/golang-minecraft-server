package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

func testItemID(t *testing.T, name string) int32 {
	t.Helper()
	id, ok := world.ItemByName(name)
	if !ok {
		t.Fatalf("unknown item %q", name)
	}
	return id
}

// TestClickOverSpectatorAndUIItems covers the connection-level guards: a
// hub UI item can't be thrown out of a window.
func TestClickRefusesThrowingUIItems(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Hubber", player.Survival, 0, 64, 0)
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: testItemID(t, "minecraft:blaze_rod"), Count: 1, Name: inventory.NavigatorName})
	clickIn(t, c, 0, int16(inventory.HotbarStart), 1, inventory.ClickThrow)
	if c.inv.Get(inventory.HotbarStart).Name != inventory.NavigatorName {
		t.Fatal("the navigator was thrown out of the inventory")
	}
}
