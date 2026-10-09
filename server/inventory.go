package server

import (
	"bytes"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

// maxStackSize is the vanilla stack ceiling for the resource items BedWars
// generators hand out (ingots, diamonds, emeralds). We don't model the
// per-item max (some items cap at 16), so this is the conservative 64.
const maxStackSize = 64

// inventory.go is the per-connection player inventory model. It mirrors the
// window-0 layout the client uses so the server knows what the player is
// actually holding — used to place only a held block (no more always-stone)
// and to pick the right throwable. It's kept in sync from creative slot edits
// and container clicks; survival pickup/crafting isn't modelled yet.
//
// Window-0 slot layout (1.20.1):
//
//	0      crafting result
//	1..4   crafting grid
//	5..8   armor
//	9..35  main inventory (3×9)
//	36..44 hotbar (9)
//	45     offhand
const (
	playerInvSize = 46
	hotbarStart   = 36 // window-0 slot of hotbar position 0
	mainInvStart  = 9  // window-0 slot where main inventory + hotbar begin
)

// playerInventory holds one player's window-0 slots. The zero value is an
// all-empty inventory.
type playerInventory struct {
	slots [playerInvSize]itemStack
}

// set stores st at a window-0 slot (bounds-checked).
func (inv *playerInventory) set(slot int16, st itemStack) {
	if slot >= 0 && int(slot) < playerInvSize {
		inv.slots[slot] = st
	}
}

// get returns the stack at a window-0 slot (empty for out-of-range).
func (inv *playerInventory) get(slot int16) itemStack {
	if slot >= 0 && int(slot) < playerInvSize {
		return inv.slots[slot]
	}
	return itemStack{}
}

// held returns the stack in the selected hotbar slot (heldSlot 0..8).
func (inv *playerInventory) held(heldSlot int32) itemStack {
	return inv.get(int16(hotbarStart) + int16(heldSlot))
}

// onSetCreativeSlot records the item a creative player put in a slot. Reads
// Short slot + Slot(item); creative is where inventory edits are synced (the
// client is authoritative in creative), so this keeps the held item accurate.
func (c *ClientConnection) onSetCreativeSlot(packet *bytes.Buffer) {
	raw, err := protocol.ReadUShortFromBuf(packet)
	if err != nil {
		return
	}
	st, ok := readSlot(packet)
	if !ok {
		return
	}
	c.inv.set(int16(raw), st)
}

// giveItem adds count of itemID to the player's inventory. It first tops up
// existing partial stacks of the same item, then fills empty slots — hotbar
// first (window-0 slots 36..44, left to right), then the main inventory
// (9..35), like vanilla, so a picked-up sword lands in the player's hand
// rather than behind the E key. Each slot it touches is pushed to the client
// with Set Container Slot. Returns how many units did NOT fit (inventory
// full) so a dropped-item pickup can leave the remainder on the ground.
//
// This is the only path that mutates a Survival player's inventory server-
// side; creative edits flow the other way via onSetCreativeSlot.
func (c *ClientConnection) giveItem(itemID int32, count int) int {
	if count <= 0 {
		return 0
	}
	remaining := count
	// Pass 0 merges into existing stacks of itemID; pass 1 fills empties.
	for pass := 0; pass < 2 && remaining > 0; pass++ {
		for _, slot := range giveSlotOrder {
			if remaining == 0 {
				break
			}
			cur := c.inv.get(slot)
			if pass == 0 {
				if cur.empty() || cur.ID != itemID || int(cur.Count) >= maxStackSize {
					continue
				}
			} else {
				if !cur.empty() {
					continue
				}
				cur = itemStack{ID: itemID, Count: 0}
			}
			add := min(maxStackSize-int(cur.Count), remaining)
			cur.Count += byte(add)
			remaining -= add
			c.inv.set(slot, cur)
			_ = c.sendSetSlot(0, slot, cur)
		}
	}
	return remaining
}

// giveSlotOrder is the slot visiting order for giveItem: the 9 hotbar slots
// first, then the 27 main-inventory slots.
var giveSlotOrder = func() []int16 {
	order := make([]int16, 0, 36)
	for slot := int16(hotbarStart); slot < hotbarStart+9; slot++ {
		order = append(order, slot)
	}
	for slot := int16(mainInvStart); slot < hotbarStart; slot++ {
		order = append(order, slot)
	}
	return order
}()

// sendSetSlot writes a single inventory slot on the client (Set Container
// Slot). windowID 0 is the player's own inventory; slot is a window-0 index.
func (c *ClientConnection) sendSetSlot(windowID byte, slot int16, st itemStack) error {
	var buf bytes.Buffer
	buf.WriteByte(windowID)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	buf.Write(protocol.WriteShort(slot))
	if st.empty() {
		buf.Write(protocol.WriteEmptySlot())
	} else {
		buf.Write(protocol.WriteSlot(st.ID, st.Count))
	}
	return c.safeWrite(CbPlaySetContainerSlot, buf.Bytes())
}

// heldItemName returns the namespaced id of the item in the selected hotbar
// slot, or "" if the slot is empty / the item id is unknown.
func (c *ClientConnection) heldItemName() string {
	st := c.inv.held(c.heldSlot.Load())
	if st.empty() {
		return ""
	}
	name, ok := world.ItemName(st.ID)
	if !ok {
		return ""
	}
	return name
}
