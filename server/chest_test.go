package server

import (
	"bytes"
	"testing"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

func TestIsChestBlock(t *testing.T) {
	chests := []world.Block{world.Chest, world.EnderChest,
		{Name: "minecraft:trapped_chest"}}
	for _, b := range chests {
		if !isChestBlock(b) {
			t.Errorf("%s should be a chest", b.Name)
		}
	}
	for _, b := range []world.Block{world.Stone, world.Air, world.RedBed} {
		if isChestBlock(b) {
			t.Errorf("%s should not be a chest", b.Name)
		}
	}
}

func TestRightClickChestOpens(t *testing.T) {
	s := New()
	chest := world.Position{X: 1, Y: 64, Z: 1}
	s.Hub.World.SetBlock(chest, world.Chest)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Opener")
	ch := cli.startDrain()
	drainExpect(t, ch, "Opener solo", CbPlayPlayerInfoUpdate)

	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0) // hand
	p.Write(protocol.WritePosition(chest.X, chest.Y, chest.Z))
	protocol.WriteVarInt32ToBuffer(&p, 1) // face +Y
	p.Write(protocol.WriteFloat(0.5))
	p.Write(protocol.WriteFloat(1.0))
	p.Write(protocol.WriteFloat(0.5))
	p.WriteByte(0)                        // inside_block
	protocol.WriteVarInt32ToBuffer(&p, 7) // sequence
	cli.write(t, SbPlayUseItemOnBlock, p.Bytes())

	// Ack the click, then the chest GUI (Open Screen + contents). No
	// Block Update — nothing was placed.
	drainExpect(t, ch, "chest opens",
		CbPlayAckBlockChange, CbPlayOpenScreen, CbPlaySetContainerContent)

	conn := findConn(t, s, "Opener")
	if conn.menu.Load() == nil {
		t.Error("server-side menu state not set after opening chest")
	}
}

func TestReadSlotSkipsNBT(t *testing.T) {
	var buf bytes.Buffer
	// A named item (carries NBT) followed by a marker varint. If readSlot
	// skips the NBT correctly, the marker reads back intact.
	buf.Write(protocol.WriteSlotWithName(800, 5, "Sword"))
	protocol.WriteVarInt32ToBuffer(&buf, 12345)

	st, ok := readSlot(&buf)
	if !ok || st.ID != 800 || st.Count != 5 {
		t.Fatalf("readSlot: %+v ok=%v", st, ok)
	}
	marker, err := protocol.ReadVarInt(&buf)
	if err != nil || marker != 12345 {
		t.Errorf("buffer misaligned after NBT skip: marker=%d err=%v", marker, err)
	}
}

func TestReadSlotEmpty(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(protocol.WriteEmptySlot())
	st, ok := readSlot(&buf)
	if !ok || !st.empty() {
		t.Errorf("empty slot: %+v ok=%v", st, ok)
	}
}

func TestApplyChestClickPersists(t *testing.T) {
	inst := NewInstance("chest-test", New(), world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	c := &ClientConnection{instance: inst}
	pos := world.Position{X: 2, Y: 64, Z: 3}

	// Client reports: chest slot 0 now holds diamond(764) ×3; cursor empty.
	put := changedSlots(t, map[int16]itemStack{0: {ID: 764, Count: 3}})
	c.applyChestClick(put, pos)

	got := inst.chestAt(pos)
	if got[0].ID != 764 || got[0].Count != 3 {
		t.Fatalf("slot 0 = %+v, want diamond×3", got[0])
	}

	// Now the client empties slot 0 (took the item out).
	clear := changedSlots(t, map[int16]itemStack{0: {}})
	c.applyChestClick(clear, pos)
	if !inst.chestAt(pos)[0].empty() {
		t.Errorf("slot 0 should be empty after removal: %+v", inst.chestAt(pos)[0])
	}

	// Player-inventory slots (>= 27) are ignored, not stored as chest slots.
	inv := changedSlots(t, map[int16]itemStack{30: {ID: 1, Count: 1}})
	c.applyChestClick(inv, pos)
	for _, s := range inst.chestAt(pos) {
		if !s.empty() {
			t.Errorf("inventory-slot change leaked into chest: %+v", s)
		}
	}
}

// changedSlots builds a Click Container changed-slots array + empty cursor,
// the suffix applyChestClick parses (after the header).
func changedSlots(t *testing.T, slots map[int16]itemStack) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(slots)))
	for slot, st := range slots {
		buf.Write(protocol.WriteShort(slot))
		if st.empty() {
			buf.Write(protocol.WriteEmptySlot())
		} else {
			buf.Write(protocol.WriteSlot(st.ID, st.Count))
		}
	}
	buf.Write(protocol.WriteEmptySlot()) // cursor
	return &buf
}
