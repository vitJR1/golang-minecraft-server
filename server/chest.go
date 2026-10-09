package server

import (
	"bytes"
	"sort"
	"sync"

	"minecraft-server/nbt"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// chest.go gives chest blocks a stored, persistent inventory. Opening a chest
// streams its saved contents; the player rearranging items (any click mode)
// is persisted by trusting the client's "changed slots" array in the Click
// Container packet — fine for a creative/from-scratch server with no anti-
// cheat. Regular chests store per (instance, block position); an ender chest
// is one 27-slot inventory per player, shared by every ender chest block in
// every instance (enderChestStore on Server). There's no item NBT model, so
// stacks are just (item id, count) plus the tool fields below.

// chestSlotCount is a single chest's slot count (generic_9x3).
const chestSlotCount = 27

// chestRows is the GUI row count for a single chest.
const chestRows = 3

// itemStack is one inventory slot's contents. Count 0 means the slot is empty
// (ID is then ignored).
type itemStack struct {
	ID    int32
	Count byte
	// Name is an optional custom display name (server-given UI items such as
	// the "Navigator" blaze rod).
	Name string
	// Damage is the wear on a tool (NBT "Damage"); the item breaks when it
	// reaches world.ToolDurability.
	Damage int
	// Enchantments maps enchantment id ("minecraft:efficiency") → level,
	// from the item's NBT "Enchantments" list. Only read, never granted.
	Enchantments map[string]int
}

func (s itemStack) empty() bool { return s.Count == 0 }

// enchantLevel returns the level of an enchantment on the stack (0 = none).
func (s itemStack) enchantLevel(id string) int { return s.Enchantments[id] }

// tag rebuilds the stack's NBT (name, damage, enchantments); nil when plain.
func (s itemStack) tag() nbt.Compound {
	var tag nbt.Compound
	set := func(k string, v nbt.Value) {
		if tag == nil {
			tag = nbt.Compound{}
		}
		tag[k] = v
	}
	if s.Name != "" {
		set("display", protocol.DisplayNameTag(s.Name))
	}
	if s.Damage > 0 {
		set("Damage", nbt.Int(int32(s.Damage)))
	}
	if len(s.Enchantments) > 0 {
		ids := make([]string, 0, len(s.Enchantments))
		for id := range s.Enchantments {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		list := nbt.List{ElemTag: nbt.TagCompound}
		for _, id := range ids {
			list.Items = append(list.Items, nbt.Compound{"id": nbt.String(id), "lvl": nbt.Short(int16(s.Enchantments[id]))})
		}
		set("Enchantments", list)
	}
	return tag
}

// writeStack appends the wire Slot for st (empty, plain, or tagged).
func writeStack(buf *bytes.Buffer, st itemStack) {
	if st.empty() {
		buf.Write(protocol.WriteEmptySlot())
		return
	}
	buf.Write(protocol.WriteSlotTagged(st.ID, st.Count, st.tag()))
}

// stackFromTag fills Damage/Enchantments/Name from a slot's NBT compound.
func stackFromTag(st itemStack, tag nbt.Compound) itemStack {
	if d, ok := tag["Damage"].(nbt.Int); ok {
		st.Damage = int(d)
	}
	if list, ok := tag["Enchantments"].(nbt.List); ok {
		for _, item := range list.Items {
			ench, ok := item.(nbt.Compound)
			if !ok {
				continue
			}
			id, _ := ench["id"].(nbt.String)
			if id == "" {
				continue
			}
			lvl := 0
			switch v := ench["lvl"].(type) {
			case nbt.Short:
				lvl = int(v)
			case nbt.Int:
				lvl = int(v)
			case nbt.Byte:
				lvl = int(v)
			}
			if lvl > 0 {
				if st.Enchantments == nil {
					st.Enchantments = map[string]int{}
				}
				st.Enchantments[string(id)] = lvl
			}
		}
	}
	return st
}

// chestInventory is one chest's stored slots.
type chestInventory struct {
	slots [chestSlotCount]itemStack
}

// chestStore is what an open chest GUI reads from and writes to: a block
// chest bound to a position, or a player's ender chest.
type chestStore interface {
	// contents returns a copy of the 27 stored slots.
	contents() [chestSlotCount]itemStack
	// setSlot stores st at idx (0..26); out-of-range is ignored.
	setSlot(idx int, st itemStack)
	// title is the GUI window title.
	title() string
}

// blockChest is the chestStore for a regular chest block in an instance.
type blockChest struct {
	inst *Instance
	pos  world.Position
}

func (b blockChest) contents() [chestSlotCount]itemStack { return b.inst.chestAt(b.pos) }
func (b blockChest) setSlot(idx int, st itemStack)       { b.inst.setChestSlot(b.pos, idx, st) }
func (b blockChest) title() string                       { return "Chest" }

// enderChest is a player's private 27 slots. One record per player UUID lives
// on the Server so it follows them across instances; every ender chest block
// opens the same record. Loaded from Server.EnderChests on first access and
// saved back after every click (ender_persist.go).
type enderChest struct {
	uuid   [16]byte
	noSave bool // set when the load failed, so we never clobber saved data
	mu     sync.Mutex
	slots  [chestSlotCount]itemStack
}

func (e *enderChest) contents() [chestSlotCount]itemStack {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.slots
}

func (e *enderChest) setSlot(idx int, st itemStack) {
	if idx < 0 || idx >= chestSlotCount {
		return
	}
	e.mu.Lock()
	e.slots[idx] = st
	e.mu.Unlock()
}

func (e *enderChest) title() string { return "Ender Chest" }

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
func (i *Instance) chestAt(pos world.Position) [chestSlotCount]itemStack {
	i.chestsMu.Lock()
	defer i.chestsMu.Unlock()
	if inv := i.chests[pos]; inv != nil {
		return inv.slots
	}
	return [chestSlotCount]itemStack{}
}

// setChestSlot stores stack in chest pos's slot idx (idx in 0..26).
func (i *Instance) setChestSlot(pos world.Position, idx int, stack itemStack) {
	if idx < 0 || idx >= chestSlotCount {
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
	c.menu.Store(&openMenu{kind: "chest", chest: store})
	_ = c.sendOpenScreen(store.title(), chestRows)
	_ = c.sendChestInventory(store.contents())
}

// sendChestInventory streams a chest's contents (Set Container Content): the 27
// chest slots from `slots`, then the player's real inventory (main + hotbar
// mirror from c.inv), then the empty cursor.
func (c *ClientConnection) sendChestInventory(slots [chestSlotCount]itemStack) error {
	var buf bytes.Buffer
	buf.WriteByte(menuWindowID)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	protocol.WriteVarInt32ToBuffer(&buf, int32(chestSlotCount+36))
	for s := 0; s < chestSlotCount; s++ {
		writeStack(&buf, slots[s])
	}
	c.writeInventoryMirror(&buf)
	buf.Write(protocol.WriteEmptySlot()) // cursor
	return c.safeWrite(CbPlaySetContainerContent, buf.Bytes())
}

// applyChestClick parses a Click Container packet's changed-slots array (after
// the window/state/slot/button/mode header has been read) and persists any
// changes to chest slots (0..26) of the open chest store; slots in the player
// inventory range update the player's own inventory model. The client
// computes the result of every click mode, so trusting its array gives
// correct chest contents without re-implementing inventory logic.
//
// Returns the carried (cursor) stack the client reports after the click, so
// the caller can track what the player is holding between clicks.
func (c *ClientConnection) applyChestClick(packet *bytes.Buffer, store chestStore) itemStack {
	count, err := protocol.ReadVarInt(packet)
	if err != nil || count < 0 {
		return c.cursor
	}
	for n := 0; n < count; n++ {
		raw, err := protocol.ReadUShortFromBuf(packet)
		if err != nil {
			return c.cursor
		}
		slot := int16(raw)
		st, ok := readSlot(packet)
		if !ok {
			return c.cursor // malformed slot — stop, leave what we have
		}
		switch {
		case slot >= 0 && slot < chestSlotCount:
			store.setSlot(int(slot), st)
		case slot >= chestSlotCount:
			// Player-inventory side of the chest window: slot chestSlotCount+i
			// maps to window-0 slot mainInvStart+i — keep the held item in sync.
			c.inv.set(slot-chestSlotCount+mainInvStart, c.inv.withKnownName(st))
		}
	}
	cursor, ok := readSlot(packet)
	if !ok {
		return c.cursor
	}
	return cursor
}

// readSlot reads one wire Slot from buf: present bool, then (item id, count,
// NBT). The NBT is skipped via nbt.SkipTag. Returns the stack (Count 0 when the
// slot is absent) and whether the read succeeded.
func readSlot(buf *bytes.Buffer) (itemStack, bool) {
	present, err := protocol.ReadBool(buf)
	if err != nil {
		return itemStack{}, false
	}
	if !present {
		return itemStack{}, true // empty slot
	}
	id, err := protocol.ReadVarInt(buf)
	if err != nil {
		return itemStack{}, false
	}
	count, err := buf.ReadByte()
	if err != nil {
		return itemStack{}, false
	}
	// Measure the item's optional NBT, then decode it for the fields we
	// model (Damage, Enchantments); the custom name is re-attached by the
	// inventory model (withKnownName) for server-given items.
	r := bytes.NewReader(buf.Bytes())
	before := r.Len()
	if err := nbt.SkipTag(r); err != nil {
		return itemStack{}, false
	}
	raw := buf.Next(before - r.Len())
	st := itemStack{ID: int32(id), Count: count}
	if len(raw) > 1 {
		if tag, err := nbt.Unmarshal(raw); err == nil {
			st = stackFromTag(st, tag)
		}
	}
	return st, true
}
