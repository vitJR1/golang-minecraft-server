package server

import (
	"bytes"
	"testing"

	"minecraft-server/player"
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
	c.applyChestClick(put, blockChest{inst, pos})

	got := inst.chestAt(pos)
	if got[0].ID != 764 || got[0].Count != 3 {
		t.Fatalf("slot 0 = %+v, want diamond×3", got[0])
	}

	// Now the client empties slot 0 (took the item out).
	clear := changedSlots(t, map[int16]itemStack{0: {}})
	c.applyChestClick(clear, blockChest{inst, pos})
	if !inst.chestAt(pos)[0].empty() {
		t.Errorf("slot 0 should be empty after removal: %+v", inst.chestAt(pos)[0])
	}

	// Player-inventory slots (>= 27) are ignored, not stored as chest slots.
	inv := changedSlots(t, map[int16]itemStack{30: {ID: 1, Count: 1}})
	c.applyChestClick(inv, blockChest{inst, pos})
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

// enderTestConn builds an offline connection (no network goroutines) for a
// player with the given UUID, parked in inst.
func enderTestConn(s *Server, inst *Instance, name string, uuid [16]byte) *ClientConnection {
	return &ClientConnection{
		server:   s,
		instance: inst,
		player:   player.New(1, name, uuid),
		outbound: make(chan outboundMsg, 64),
		done:     make(chan struct{}),
	}
}

// TestEnderChestSharedPerPlayer: two ender chest blocks in two different
// instances open the same per-player inventory, and another player sees
// their own, separate one.
func TestEnderChestSharedPerPlayer(t *testing.T) {
	s := New()
	other := NewInstance("arena", s, world.NewMemoryWorld())
	t.Cleanup(other.Stop)
	ender := world.Block{StateID: 1, Name: "minecraft:ender_chest"}
	hubPos := world.Position{X: 1, Y: 64, Z: 1}
	arenaPos := world.Position{X: 9, Y: 64, Z: 9}
	s.Hub.World.SetBlock(hubPos, ender)
	other.World.SetBlock(arenaPos, ender)

	owner := [16]byte{1}
	c := enderTestConn(s, s.Hub, "Owner", owner)

	// Open the hub ender chest and put a diamond in slot 0.
	c.openBlockChest(hubPos)
	m := c.menu.Load()
	if m == nil || m.kind != "chest" {
		t.Fatalf("menu after opening ender chest: %+v", m)
	}
	if _, ok := m.chest.(*enderChest); !ok {
		t.Fatalf("ender chest block should open an enderChest store, got %T", m.chest)
	}
	c.applyChestClick(changedSlots(t, map[int16]itemStack{0: {ID: 764, Count: 3}}), m.chest)

	// The same player in another instance sees the same slots; the hub
	// block's own position holds nothing as a regular chest.
	c2 := enderTestConn(s, other, "Owner", owner)
	c2.openBlockChest(arenaPos)
	got := c2.menu.Load().chest.contents()
	if got[0].ID != 764 || got[0].Count != 3 {
		t.Fatalf("arena ender chest slot 0 = %+v, want diamond×3", got[0])
	}
	if !s.Hub.chestAt(hubPos)[0].empty() {
		t.Error("ender contents leaked into the block-position chest store")
	}

	// Another player has their own empty ender chest.
	stranger := enderTestConn(s, s.Hub, "Stranger", [16]byte{2})
	stranger.openBlockChest(hubPos)
	if st := stranger.menu.Load().chest.contents()[0]; !st.empty() {
		t.Errorf("stranger sees owner's items: %+v", st)
	}
	if s.enderChestFor(owner) != m.chest {
		t.Error("enderChestFor should return the same record for the same player")
	}
	// A regular chest still opens a block-bound store.
	regular := world.Position{X: 3, Y: 64, Z: 3}
	s.Hub.World.SetBlock(regular, world.Chest)
	c.openBlockChest(regular)
	if _, ok := c.menu.Load().chest.(blockChest); !ok {
		t.Errorf("regular chest should open a blockChest, got %T", c.menu.Load().chest)
	}
}
