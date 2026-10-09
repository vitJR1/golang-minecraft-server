package server

import (
	"testing"

	"minecraft-server/world"
)

func TestGiveItemFillsHotbarBeforeMainInventory(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Picker")
	cli.startDiscardDrain()
	c := findConn(t, s, "Picker")

	iron, _ := world.ItemByName("minecraft:iron_ingot")
	sword, _ := world.ItemByName("minecraft:iron_sword")

	// Empty inventory: the first pickup goes to hotbar slot 0.
	if left := c.giveItem(iron, 3); left != 0 {
		t.Fatalf("leftover: %d", left)
	}
	if st := c.inv.get(hotbarStart); st.ID != iron || st.Count != 3 {
		t.Errorf("hotbar slot 0: %+v, want 3 iron", st)
	}
	if st := c.inv.get(mainInvStart); !st.empty() {
		t.Errorf("main inventory must stay empty, got %+v", st)
	}

	// A different item takes the next free hotbar slot.
	c.giveItem(sword, 1)
	if st := c.inv.get(hotbarStart + 1); st.ID != sword {
		t.Errorf("hotbar slot 1: %+v, want sword", st)
	}

	// Existing partial stacks are topped up (hotbar stack first, then a
	// main-inventory stack) before any empty slot is used.
	c.inv.set(mainInvStart+5, itemStack{ID: iron, Count: 60})
	c.giveItem(iron, 65)
	if st := c.inv.get(hotbarStart); st.Count != 64 {
		t.Errorf("hotbar stack should be topped up to 64 first, got %d", st.Count)
	}
	if st := c.inv.get(mainInvStart + 5); st.Count != 64 {
		t.Errorf("main stack should be topped up to 64 next, got %d", st.Count)
	}
	if st := c.inv.get(hotbarStart + 2); !st.empty() {
		t.Errorf("no leftover iron expected in a new slot, got %+v", st)
	}

	// Hotbar full → a new item spills into the main inventory, first free slot.
	for slot := int16(hotbarStart + 2); slot < hotbarStart+9; slot++ {
		c.inv.set(slot, itemStack{ID: sword, Count: 1})
	}
	diamond, _ := world.ItemByName("minecraft:diamond")
	c.giveItem(diamond, 1)
	if st := c.inv.get(mainInvStart); st.ID != diamond {
		t.Errorf("overflow should land in main slot 9, got %+v", st)
	}
}
