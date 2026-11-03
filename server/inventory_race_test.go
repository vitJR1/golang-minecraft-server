package server

import (
	"sync"
	"testing"

	"minecraft-server/player"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// TestPickupsDuringClicksKeepTheTotal: the tick goroutine gives items
// (generator pickups) while the readLoop simulates clicks. A click used to
// copy the slots, simulate and copy back, overwriting a pickup that landed
// in between (lost) or bringing back a spent stack (duplicated).
func TestPickupsDuringClicksKeepTheTotal(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Clicker", player.Survival, 0, 64, 0)
	apple, _ := world.ItemByName("minecraft:apple")
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: apple, Count: 10})

	const gives = 200
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range gives {
			c.GiveStack(inventory.Stack{ID: apple, Count: 1})
		}
	}()
	for range 300 {
		clickIn(t, c, 0, int16(inventory.HotbarStart), 0, inventory.ClickPickup)
		clickIn(t, c, 0, int16(inventory.MainStart), 0, inventory.ClickPickup)
	}
	wg.Wait()
	drain(c)

	if got := c.countItem(apple) + int(c.inv.Cursor().Count); got != 10+gives {
		t.Fatalf("apples: %d, want %d", got, 10+gives)
	}
}
