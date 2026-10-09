package server

import (
	"bytes"
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

func TestGiveItemFillsHotbarBeforeMainInventory(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Picker", player.Survival, 0, 64, 0)

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

func TestCountAndTakeItem(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Buyer", player.Survival, 0, 64, 0)

	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(hotbarStart, itemStack{ID: iron, Count: 10})
	c.inv.set(mainInvStart+3, itemStack{ID: iron, Count: 20})
	if got := c.countItem(iron); got != 30 {
		t.Fatalf("count: %d, want 30", got)
	}
	if c.takeItem(iron, 31) {
		t.Error("taking more than held must fail")
	}
	if got := c.countItem(iron); got != 30 {
		t.Errorf("failed take must not change inventory, got %d", got)
	}
	// Main inventory is drained before the hotbar.
	if !c.takeItem(iron, 25) {
		t.Fatal("take 25 should succeed")
	}
	if st := c.inv.get(mainInvStart + 3); !st.empty() {
		t.Errorf("main stack should be gone, got %+v", st)
	}
	if st := c.inv.get(hotbarStart); st.Count != 5 {
		t.Errorf("hotbar stack: %d, want 5", st.Count)
	}
	if got := c.countItem(iron); got != 5 {
		t.Errorf("count after take: %d, want 5", got)
	}
}

func TestNavigatorStrippedOnGameJoinAndKeptInHub(t *testing.T) {
	s := New()
	SetupHubMenu(s)
	arena := NewInstance("arena", s, world.NewMemoryWorld())
	t.Cleanup(arena.Stop)
	s.AddInstance(arena)

	c := offlineHub(s, "Gamer")
	if c.inv.get(hotbarStart).Name != navigatorName {
		t.Fatal("hub join should hand out the navigator")
	}

	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.giveItem(iron, 5) // lands in hotbar slot 1 (slot 0 holds the rod)

	if err := s.MovePlayer(c, arena, 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	drain(c)
	if st := c.inv.get(hotbarStart); !st.empty() {
		t.Errorf("navigator should be stripped in a game instance, got %+v", st)
	}
	if st := c.inv.get(hotbarStart + 1); st.ID != iron || st.Count != 5 {
		t.Errorf("real items must survive the move, got %+v", st)
	}

	// Back to the hub: the rod is handed out again, the iron stays.
	if err := s.MovePlayer(c, s.Hub, 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	drain(c)
	if c.inv.get(hotbarStart).Name != navigatorName {
		t.Error("hub join should hand out the navigator again")
	}
	if st := c.inv.get(hotbarStart + 1); st.ID != iron {
		t.Errorf("iron lost on return to hub: %+v", st)
	}
}

func TestWindowZeroClickUpdatesModel(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Mover", player.Survival, 0, 64, 0)

	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(hotbarStart, itemStack{ID: iron, Count: 1})
	c.inv.set(hotbarStart+1, itemStack{ID: iron, Count: 63})

	// Client merged slot 36 into slot 37: window 0, changed slots
	// {36: empty, 37: 64 iron}, empty cursor.
	clickSlot(t, c, int16(hotbarStart), 0,
		map[int16]itemStack{int16(hotbarStart): {}, int16(hotbarStart + 1): {ID: iron, Count: 64}}, itemStack{})
	if !c.inv.get(hotbarStart).empty() || c.inv.get(hotbarStart+1).Count != 64 {
		t.Errorf("model after merge: %+v / %+v", c.inv.get(hotbarStart), c.inv.get(hotbarStart+1))
	}
	if got := c.countItem(iron); got != 64 {
		t.Errorf("count after merge: %d", got)
	}
}

func TestMenuWindowMirrorsRealInventory(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Shopper", player.Survival, 0, 64, 0)
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(mainInvStart+2, itemStack{ID: iron, Count: 7})
	c.inv.set(hotbarStart+4, itemStack{ID: iron, Count: 9})

	var buf bytes.Buffer
	c.writeInventoryMirror(&buf)
	// 36 wire slots: parse them back and check the two stacks are present
	// at the expected positions (main index 2, hotbar index 4 → 27+4).
	for i := 0; i < 36; i++ {
		st, ok := readSlot(&buf)
		if !ok {
			t.Fatalf("slot %d unreadable", i)
		}
		switch i {
		case 2:
			if st.ID != iron || st.Count != 7 {
				t.Errorf("main slot 2: %+v", st)
			}
		case 27 + 4:
			if st.ID != iron || st.Count != 9 {
				t.Errorf("hotbar slot 4: %+v", st)
			}
		default:
			if !st.empty() {
				t.Errorf("slot %d should be empty, got %+v", i, st)
			}
		}
	}
}
