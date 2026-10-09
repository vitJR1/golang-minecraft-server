package server

import (
	"bytes"
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

// itemsIn snapshots (itemID → total units) of every dropped item in inst.
func itemsIn(inst *Instance) map[int32]int {
	inst.itemsMu.Lock()
	defer inst.itemsMu.Unlock()
	out := map[int32]int{}
	for _, it := range inst.items {
		out[it.stack.ID] += int(it.stack.Count)
	}
	return out
}

func dropAction(t *testing.T, c *ClientConnection, action int32) {
	t.Helper()
	digAt(t, c, world.Position{}, action)
}

func TestQDropsOneThenCtrlQDropsStack(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	c := offlineConn(inst, "Dropper", player.Survival, 0.5, 64, 0.5)
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(hotbarStart, itemStack{ID: iron, Count: 10})

	dropAction(t, c, 4) // Q
	if itemsIn(inst)[iron] != 1 {
		t.Errorf("Q should drop one unit, ground has %v", itemsIn(inst))
	}
	if st := c.inv.get(hotbarStart); st.Count != 9 {
		t.Errorf("held stack after Q: %+v, want 9", st)
	}

	dropAction(t, c, 3) // ctrl+Q
	if itemsIn(inst)[iron] != 10 {
		t.Errorf("ctrl+Q should drop the rest, ground has %v", itemsIn(inst))
	}
	if st := c.inv.get(hotbarStart); !st.empty() {
		t.Errorf("held stack after ctrl+Q: %+v, want empty", st)
	}

	// The thrower can't vacuum it straight back: still on the ground after
	// a few ticks even though the player stands right there.
	for i := 0; i < 5; i++ {
		inst.itemTick(uint64(i))
	}
	if got := itemsIn(inst)[iron]; got != 10 {
		t.Errorf("thrown items picked up too early: %d left on the ground", got)
	}
}

func TestNavigatorNotDroppable(t *testing.T) {
	s := New()
	SetupHubMenu(s)
	c := offlineHub(s, "Keeper")
	if c.inv.get(hotbarStart).Name != navigatorName {
		t.Fatal("hub join should hand out the navigator")
	}
	dropAction(t, c, 3)
	if c.inv.get(hotbarStart).Name != navigatorName || len(itemsIn(s.Hub)) != 0 {
		t.Error("navigator must stay in the hotbar and never become an item entity")
	}
}

func TestCursorDropOutsideWindowAndOnClose(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	c := offlineConn(inst, "Cursor", player.Survival, 0.5, 64, 0.5)
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(hotbarStart, itemStack{ID: iron, Count: 8})

	// Pick the stack up onto the cursor: nothing dropped yet.
	clickSlot(t, c, int16(hotbarStart), 0, map[int16]itemStack{int16(hotbarStart): {}}, itemStack{ID: iron, Count: 8})
	if c.cursor.Count != 8 {
		t.Fatalf("cursor should hold 8, got %+v", c.cursor)
	}
	if n := itemsIn(inst)[iron]; n != 0 {
		t.Fatalf("pickup onto cursor must not drop, got %d", n)
	}

	// Right-click outside the window: one unit leaves the cursor.
	clickSlot(t, c, -999, 0, nil, itemStack{ID: iron, Count: 7})
	if itemsIn(inst)[iron] != 1 {
		t.Errorf("one unit should drop from the cursor, ground has %v", itemsIn(inst))
	}

	// Close the inventory with 7 still on the cursor: all of it drops.
	var p bytes.Buffer
	p.WriteByte(0)
	play(t, c, SbPlayCloseContainer, p.Bytes())
	if itemsIn(inst)[iron] != 8 {
		t.Errorf("closing should drop the cursor, ground has %v", itemsIn(inst))
	}
	if !c.cursor.empty() {
		t.Errorf("cursor should be cleared on close, got %+v", c.cursor)
	}
}

func TestDropKeyOverSlotInWindow(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	c := offlineConn(inst, "Keyed", player.Survival, 0.5, 64, 0.5)
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(mainInvStart, itemStack{ID: iron, Count: 5})

	// Drop key over the slot (mode 4): the client reports the slot shrunk.
	clickSlot(t, c, int16(mainInvStart), 4, map[int16]itemStack{int16(mainInvStart): {ID: iron, Count: 4}}, itemStack{})
	if itemsIn(inst)[iron] != 1 {
		t.Errorf("drop key should drop one, ground has %v", itemsIn(inst))
	}
	if st := c.inv.get(mainInvStart); st.Count != 4 {
		t.Errorf("slot after drop key: %+v", st)
	}
}

func TestThrownItemFliesForwardAndSlides(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	for x := -5; x <= 5; x++ {
		for z := -5; z <= 5; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	if !inst.ThrowItem(iron, 1, 0.5, 65.3, 0.5, 0, 0.1, 0.3) {
		t.Fatal("throw failed")
	}
	for i := 0; i < 60; i++ {
		inst.itemTick(uint64(i))
	}
	inst.itemsMu.Lock()
	it := inst.items[0]
	inst.itemsMu.Unlock()
	if !it.onGround || it.y != 64 {
		t.Errorf("item should rest on the floor: y=%v onGround=%v", it.y, it.onGround)
	}
	if it.z < 1.5 {
		t.Errorf("item should have travelled along +Z, got z=%v", it.z)
	}
	if it.vx != 0 || it.vz != 0 {
		t.Errorf("item should have stopped sliding, got v=(%v,%v)", it.vx, it.vz)
	}
}
