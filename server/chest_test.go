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

// chestHotbar0 is hotbar slot 0 inside a chest window (27 chest slots, then
// the player's main inventory, then the hotbar).
const chestHotbar0 = chestSlotCount + 27

func TestChestClickPersists(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Stasher", player.Survival, 0, 64, 0)
	pos := world.Position{X: 2, Y: 64, Z: 3}
	c.inv.set(hotbarStart, itemStack{ID: 764, Count: 3})
	c.openBlockChest(pos)
	drain(c)

	// Pick the diamonds up from the hotbar, drop them into chest slot 0.
	clickIn(t, c, menuWindowID, chestHotbar0, 0, clickPickup)
	clickIn(t, c, menuWindowID, 0, 0, clickPickup)
	if got := inst.chestAt(pos)[0]; got.ID != 764 || got.Count != 3 {
		t.Fatalf("chest slot 0 = %+v, want diamond×3", got)
	}
	if !c.inv.get(hotbarStart).empty() || !c.cursor.empty() {
		t.Fatalf("diamonds duplicated: hotbar %+v cursor %+v", c.inv.get(hotbarStart), c.cursor)
	}

	// Shift-click them back: into the player's inventory, chest empty.
	clickIn(t, c, menuWindowID, 0, 0, clickQuickMove)
	if !inst.chestAt(pos)[0].empty() {
		t.Errorf("chest slot 0 should be empty after shift-click: %+v", inst.chestAt(pos)[0])
	}
	if got := c.countItem(764); got != 3 {
		t.Errorf("player holds %d diamonds, want 3", got)
	}
}

// TestChestClickIgnoresClaimedItems: the client's "changed slots" can't put
// anything into a chest — only the simulated click counts.
func TestChestClickIgnoresClaimedItems(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Forger", player.Survival, 0, 64, 0)
	pos := world.Position{X: 2, Y: 64, Z: 3}
	c.openBlockChest(pos)
	drain(c)

	// Empty cursor, empty slot — the click does nothing, whatever the
	// packet claims.
	clickWith(t, c, menuWindowID, 0, 0, clickPickup,
		map[int16]itemStack{0: {ID: 764, Count: 64}, chestHotbar0: {ID: 764, Count: 64}},
		itemStack{ID: 764, Count: 64})
	if got := inst.chestAt(pos)[0]; !got.empty() {
		t.Fatalf("claimed diamonds stored in the chest: %+v", got)
	}
	if c.countItem(764) != 0 || !c.cursor.empty() {
		t.Fatalf("claimed diamonds reached the player: %d, cursor %+v", c.countItem(764), c.cursor)
	}
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

	// Open the hub ender chest and put the player's diamonds in slot 0.
	c.inv.set(hotbarStart, itemStack{ID: 764, Count: 3})
	c.openBlockChest(hubPos)
	m := c.menu.Load()
	if m == nil || m.kind != "chest" {
		t.Fatalf("menu after opening ender chest: %+v", m)
	}
	if _, ok := m.chest.(*enderChest); !ok {
		t.Fatalf("ender chest block should open an enderChest store, got %T", m.chest)
	}
	clickIn(t, c, menuWindowID, chestHotbar0, 0, clickPickup)
	clickIn(t, c, menuWindowID, 0, 0, clickPickup)

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
