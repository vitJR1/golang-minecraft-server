package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

// itemsIn snapshots (itemID → total units) of every dropped item in inst.
func itemsIn(inst *Instance) map[int32]int {
	inst.itemsMu.Lock()
	defer inst.itemsMu.Unlock()
	out := map[int32]int{}
	for _, it := range inst.items {
		out[it.itemID] += it.count
	}
	return out
}

func playerAction(t *testing.T, cli *testClient, action int32) {
	t.Helper()
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, action)
	p.Write(protocol.WritePosition(0, 0, 0))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 1)
	cli.write(t, SbPlayPlayerAction, p.Bytes())
}

func TestQDropsOneThenCtrlQDropsStack(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Dropper")
	cli.startDiscardDrain()
	c := findConn(t, s, "Dropper")
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(hotbarStart, itemStack{ID: iron, Count: 10})

	playerAction(t, cli, 4) // Q
	waitFor(t, time.Second, func() bool { return itemsIn(s.Hub)[iron] == 1 }, "Q to drop one unit")
	if st := c.inv.get(hotbarStart); st.Count != 9 {
		t.Errorf("held stack after Q: %+v, want 9", st)
	}

	playerAction(t, cli, 3) // ctrl+Q
	waitFor(t, time.Second, func() bool { return itemsIn(s.Hub)[iron] == 10 }, "ctrl+Q to drop the rest")
	if st := c.inv.get(hotbarStart); !st.empty() {
		t.Errorf("held stack after ctrl+Q: %+v, want empty", st)
	}

	// The thrower can't vacuum it straight back: still on the ground after
	// a few ticks even though the player stands on top of it.
	time.Sleep(150 * time.Millisecond)
	if got := itemsIn(s.Hub)[iron]; got != 10 {
		t.Errorf("thrown items picked up too early: %d left on the ground", got)
	}
}

func TestNavigatorNotDroppable(t *testing.T) {
	s := New()
	SetupHubMenu(s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Keeper")
	cli.startDiscardDrain()
	c := findConn(t, s, "Keeper")
	waitFor(t, time.Second, func() bool { return c.inv.get(hotbarStart).Name == navigatorName }, "navigator")

	playerAction(t, cli, 3)
	time.Sleep(50 * time.Millisecond)
	if c.inv.get(hotbarStart).Name != navigatorName || len(itemsIn(s.Hub)) != 0 {
		t.Error("navigator must stay in the hotbar and never become an item entity")
	}
}

// clickWindow0 sends a Click Container for window 0 with the given changed
// slots and resulting cursor.
func clickWindow0(t *testing.T, cli *testClient, slot int16, mode int32, changed map[int16]itemStack, cursor itemStack) {
	t.Helper()
	var p bytes.Buffer
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteShort(slot))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, mode)
	protocol.WriteVarInt32ToBuffer(&p, int32(len(changed)))
	for s, st := range changed {
		p.Write(protocol.WriteShort(s))
		writeStack(&p, st)
	}
	writeStack(&p, cursor)
	cli.write(t, SbPlayClickContainer, p.Bytes())
}

func TestCursorDropOutsideWindowAndOnClose(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Cursor")
	cli.startDiscardDrain()
	c := findConn(t, s, "Cursor")
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(hotbarStart, itemStack{ID: iron, Count: 8})

	// Pick the stack up onto the cursor: nothing dropped yet.
	clickWindow0(t, cli, int16(hotbarStart), 0, map[int16]itemStack{int16(hotbarStart): {}}, itemStack{ID: iron, Count: 8})
	waitFor(t, time.Second, func() bool { return c.cursor.Count == 8 }, "cursor pickup")
	if n := itemsIn(s.Hub)[iron]; n != 0 {
		t.Fatalf("pickup onto cursor must not drop, got %d", n)
	}

	// Right-click outside the window: one unit leaves the cursor.
	clickWindow0(t, cli, -999, 0, nil, itemStack{ID: iron, Count: 7})
	waitFor(t, time.Second, func() bool { return itemsIn(s.Hub)[iron] == 1 }, "drop one from cursor")

	// Close the inventory with 7 still on the cursor: all of it drops.
	var p bytes.Buffer
	p.WriteByte(0)
	cli.write(t, SbPlayCloseContainer, p.Bytes())
	waitFor(t, time.Second, func() bool { return itemsIn(s.Hub)[iron] == 8 }, "drop cursor on close")
	if !c.cursor.empty() {
		t.Errorf("cursor should be cleared on close, got %+v", c.cursor)
	}
}

func TestDropKeyOverSlotInWindow(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Keyed")
	cli.startDiscardDrain()
	c := findConn(t, s, "Keyed")
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(mainInvStart, itemStack{ID: iron, Count: 5})

	// Drop key over the slot (mode 4): the client reports the slot shrunk.
	clickWindow0(t, cli, int16(mainInvStart), 4, map[int16]itemStack{int16(mainInvStart): {ID: iron, Count: 4}}, itemStack{})
	waitFor(t, time.Second, func() bool { return itemsIn(s.Hub)[iron] == 1 }, "drop key over slot")
	if st := c.inv.get(mainInvStart); st.Count != 4 {
		t.Errorf("slot after drop key: %+v", st)
	}
}

func TestThrownItemFliesForwardAndSlides(t *testing.T) {
	s := New()
	inst := NewInstance("t", s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
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
