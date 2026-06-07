package server

import (
	"bytes"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

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
