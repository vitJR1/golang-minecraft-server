package server

import (
	"bytes"

	"minecraft-server/nbt"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// chest.go gives chest blocks a stored, persistent inventory. Opening a chest
// streams its saved contents; the player rearranging items (any click mode)
// is persisted by trusting the client's "changed slots" array in the Click
// Container packet — fine for a creative/from-scratch server with no anti-
// cheat. Storage lives per (instance, block position). There's no item NBT
// model, so stacks are just (item id, count).

// chestSlotCount is a single chest's slot count (generic_9x3).
const chestSlotCount = 27

// chestRows is the GUI row count for a single chest.
const chestRows = 3

// itemStack is one inventory slot's contents. Count 0 means the slot is empty
// (ID is then ignored).
type itemStack struct {
	ID    int32
	Count byte
}

func (s itemStack) empty() bool { return s.Count == 0 }

// chestInventory is one chest's stored slots.
type chestInventory struct {
	slots [chestSlotCount]itemStack
}

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

// openBlockChest opens the chest at pos for this client: it binds the menu
// window to that position and streams the chest's stored contents.
func (c *ClientConnection) openBlockChest(pos world.Position) {
	slots := c.instance.chestAt(pos)
	c.menu.Store(&openMenu{kind: "chest", chestPos: pos})
	_ = c.sendOpenScreen("Chest", chestRows)
	_ = c.sendChestInventory(slots)
}

// sendChestInventory streams a chest's contents (Set Container Content): the 27
// chest slots from `slots`, then the player-inventory mirror (so the hotbar
// items don't vanish), then the empty cursor.
func (c *ClientConnection) sendChestInventory(slots [chestSlotCount]itemStack) error {
	total := int16(chestSlotCount + 36)
	hotbarSlot0 := int16(chestSlotCount + 27)
	hotbarSlot1 := int16(chestSlotCount + 28)

	includePearl := false
	if c.instance != nil {
		_, includePearl = arenasForLobby(c.instance.ID)
	}

	var buf bytes.Buffer
	buf.WriteByte(menuWindowID)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	protocol.WriteVarInt32ToBuffer(&buf, int32(total))
	for s := int16(0); s < total; s++ {
		switch {
		case s < chestSlotCount:
			if st := slots[s]; !st.empty() {
				buf.Write(protocol.WriteSlot(st.ID, st.Count))
			} else {
				buf.Write(protocol.WriteEmptySlot())
			}
		case s == hotbarSlot0:
			buf.Write(protocol.WriteSlotWithName(itemBlazeRod, 1, "Navigator"))
		case s == hotbarSlot1 && includePearl:
			buf.Write(protocol.WriteSlotWithName(itemEnderPearl, 1, "Arena selector"))
		default:
			buf.Write(protocol.WriteEmptySlot())
		}
	}
	buf.Write(protocol.WriteEmptySlot()) // cursor
	return c.safeWrite(CbPlaySetContainerContent, buf.Bytes())
}

// applyChestClick parses a Click Container packet's changed-slots array (after
// the window/state/slot/button/mode header has been read) and persists any
// changes to chest slots (0..26) of the open chest. Slots in the player
// inventory range are ignored (we don't model the player inventory). The
// client computes the result of every click mode, so trusting its array gives
// correct chest contents without re-implementing inventory logic.
func (c *ClientConnection) applyChestClick(packet *bytes.Buffer, pos world.Position) {
	count, err := protocol.ReadVarInt(packet)
	if err != nil || count < 0 {
		return
	}
	for n := 0; n < count; n++ {
		raw, err := protocol.ReadUShortFromBuf(packet)
		if err != nil {
			return
		}
		slot := int16(raw)
		st, ok := readSlot(packet)
		if !ok {
			return // malformed slot — stop, leave what we have
		}
		switch {
		case slot >= 0 && slot < chestSlotCount:
			c.instance.setChestSlot(pos, int(slot), st)
		case slot >= chestSlotCount:
			// Player-inventory side of the chest window: slot chestSlotCount+i
			// maps to window-0 slot mainInvStart+i — keep the held item in sync.
			c.inv.set(slot-chestSlotCount+mainInvStart, st)
		}
	}
	// Trailing carried item (cursor) — read to keep parsing consistent; unused.
	_, _ = readSlot(packet)
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
	// Skip the item's optional NBT so the next slot parses correctly.
	r := bytes.NewReader(buf.Bytes())
	before := r.Len()
	if err := nbt.SkipTag(r); err != nil {
		return itemStack{}, false
	}
	buf.Next(before - r.Len())
	return itemStack{ID: int32(id), Count: count}, true
}
