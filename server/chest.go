package server

import (
	"bytes"
	"sync"

	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

// chest.go gives chest blocks a stored, persistent inventory. Opening a chest
// streams its saved contents; the player rearranging items is simulated by
// the server (click.go) and the result stored. Regular chests store per (instance, block position); an ender chest
// is one 27-slot inventory per player, shared by every ender chest block in
// every instance (enderChestStore on Server). There's no item NBT model, so
// stacks are just (item id, count) plus the tool fields below.

// chestRows is the GUI row count for a single chest.
const chestRows = 3

// chestInventory is one chest's stored slots.
type chestInventory struct {
	slots [inventory.ChestSlots]inventory.Stack
}

// chestStore is what an open chest GUI reads from and writes to: a block
// chest bound to a position, or a player's ender chest.
type chestStore interface {
	// contents returns a copy of the 27 stored slots.
	contents() [inventory.ChestSlots]inventory.Stack
	// setSlot stores st at idx (0..26); out-of-range is ignored.
	setSlot(idx int, st inventory.Stack)
	// update runs fn on the stored slots under the store's lock: a click
	// reads and writes the chest atomically (click.go).
	update(fn func(*[inventory.ChestSlots]inventory.Stack))
	// title is the GUI window title.
	title() string
}

// blockChest is the chestStore for a regular chest block in an instance.
type blockChest struct {
	inst *Instance
	pos  world.Position
}

func (b blockChest) contents() [inventory.ChestSlots]inventory.Stack { return b.inst.chestAt(b.pos) }
func (b blockChest) setSlot(idx int, st inventory.Stack)             { b.inst.setChestSlot(b.pos, idx, st) }
func (b blockChest) title() string                                   { return "Chest" }
func (b blockChest) update(fn func(*[inventory.ChestSlots]inventory.Stack)) {
	b.inst.updateChest(b.pos, fn)
}

// enderChest is a player's private 27 slots. One record per player UUID lives
// on the Server so it follows them across instances; every ender chest block
// opens the same record. Loaded from Server.EnderChests on first access and
// saved back after every click (ender_persist.go).
type enderChest struct {
	uuid   [16]byte
	noSave bool // set when the load failed, so we never clobber saved data
	mu     sync.Mutex
	slots  [inventory.ChestSlots]inventory.Stack
}

func (e *enderChest) contents() [inventory.ChestSlots]inventory.Stack {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.slots
}

func (e *enderChest) setSlot(idx int, st inventory.Stack) {
	if idx < 0 || idx >= inventory.ChestSlots {
		return
	}
	e.mu.Lock()
	e.slots[idx] = st
	e.mu.Unlock()
}

func (e *enderChest) title() string { return "Ender Chest" }

func (e *enderChest) update(fn func(*[inventory.ChestSlots]inventory.Stack)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(&e.slots)
}

// enderChestFor returns the player's ender chest, creating it on first use.
func (s *Server) enderChestFor(uuid [16]byte) *enderChest {
	s.enderMu.Lock()
	defer s.enderMu.Unlock()
	if s.enderChests == nil {
		s.enderChests = make(map[[16]byte]*enderChest)
	}
	e := s.enderChests[uuid]
	if e == nil {
		e = &enderChest{uuid: uuid}
		s.loadEnderChest(e)
		s.enderChests[uuid] = e
	}
	return e
}

// isEnderChest reports whether b is an ender chest block.
func isEnderChest(b world.Block) bool { return b.Name == "minecraft:ender_chest" }

// chestAt returns (a copy of) the chest's stored slots at pos, creating an
// empty record on first access.
func (i *Instance) chestAt(pos world.Position) [inventory.ChestSlots]inventory.Stack {
	i.chestsMu.Lock()
	defer i.chestsMu.Unlock()
	if inv := i.chests[pos]; inv != nil {
		return inv.slots
	}
	return [inventory.ChestSlots]inventory.Stack{}
}

// setChestSlot stores stack in chest pos's slot idx (idx in 0..26).
func (i *Instance) setChestSlot(pos world.Position, idx int, stack inventory.Stack) {
	if idx < 0 || idx >= inventory.ChestSlots {
		return
	}
	i.chestsMu.Lock()
	defer i.chestsMu.Unlock()
	if i.chests == nil {
		i.chests = make(map[world.Position]*chestInventory)
	}
	inv := i.chests[pos]
	if inv == nil {
		inv = &chestInventory{}
		i.chests[pos] = inv
	}
	inv.slots[idx] = stack
}

// updateChest runs fn on chest pos's slots under chestsMu, creating the
// record on first use.
func (i *Instance) updateChest(pos world.Position, fn func(*[inventory.ChestSlots]inventory.Stack)) {
	i.chestsMu.Lock()
	defer i.chestsMu.Unlock()
	if i.chests == nil {
		i.chests = make(map[world.Position]*chestInventory)
	}
	inv := i.chests[pos]
	if inv == nil {
		inv = &chestInventory{}
		i.chests[pos] = inv
	}
	fn(&inv.slots)
}

// openBlockChest opens the chest block at pos for this client: a regular
// chest binds the menu to that position; an ender chest binds it to the
// player's own ender inventory. Then the stored contents are streamed.
func (c *ClientConnection) openBlockChest(pos world.Position) {
	var store chestStore = blockChest{inst: c.instance, pos: pos}
	if isEnderChest(c.instance.World.GetBlock(pos)) && c.player != nil && c.server != nil {
		store = c.server.enderChestFor(c.player.UUID)
	}
	c.openChestStore(store)
}

// openChestStore shows store's contents in the chest GUI.
func (c *ClientConnection) openChestStore(store chestStore) {
	c.drag.Reset()
	c.menu.Store(&openMenu{kind: "chest", chest: store})
	_ = c.sendOpenScreen(store.title(), chestRows)
	_ = c.sendChestInventory(store.contents())
}

// sendChestInventory streams a chest's contents (Set Container Content): the 27
// chest slots from `slots`, then the player's real inventory (main + hotbar
// mirror from c.inv), then the cursor.
func (c *ClientConnection) sendChestInventory(slots [inventory.ChestSlots]inventory.Stack) error {
	var buf bytes.Buffer
	buf.WriteByte(menuWindowID)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	protocol.WriteVarInt32ToBuffer(&buf, int32(inventory.ChestSlots+36))
	for s := 0; s < inventory.ChestSlots; s++ {
		inventory.WriteSlot(&buf, slots[s])
	}
	c.writeInventoryMirror(&buf)
	inventory.WriteSlot(&buf, c.cursor)
	return c.safeWrite(packetid.CbPlaySetContainerContent, buf.Bytes())
}
