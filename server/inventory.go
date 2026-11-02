package server

import (
	"bytes"
	"fmt"
	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/inventory"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

// sendInventoryContents pushes the whole window-0 inventory (Set Container
// Content) from the model: after a Respawn, a cross-instance move, or a
// closed menu window, so the client's view matches the server again.
func (c *ClientConnection) sendInventoryContents() error {
	return c.safeWrite(packetid.CbPlaySetContainerContent, inventory.ContentsPayload(0, c.inv.Slots[:], c.cursor))
}

// stripNavigatorItems removes the server-given hub UI items (navigator rod,
// arena selector) from the inventory. Called when the player enters a game
// instance, where those slots belong to the game's economy.
func (c *ClientConnection) stripNavigatorItems() {
	for slot := int16(inventory.MainStart); slot < inventory.HotbarStart+9; slot++ {
		st := c.inv.Get(slot)
		if st.Name == inventory.NavigatorName || st.Name == inventory.SelectorName {
			c.inv.Set(slot, inventory.Stack{})
		}
	}
}

// onSetCreativeSlot records the item a creative player put in a slot. Reads
// Short slot + Slot(item); creative is where inventory edits are synced (the
// client is authoritative in creative), so this keeps the held item accurate.
//
// Only in creative: in any other mode this packet would conjure items out of
// thin air (a stack of emeralds for the BedWars shop), so it is refused,
// flagged, and the slot re-sent from the model to undo the client's view.
// Slot -1 (drop from the creative tab) changes no slot and is ignored.
func (c *ClientConnection) onSetCreativeSlot(packet *bytes.Buffer) {
	raw, err := protocol.ReadUShortFromBuf(packet)
	if err != nil {
		return
	}
	st, ok := inventory.ReadSlot(packet)
	if !ok {
		return
	}
	slot := int16(raw)
	inRange := slot >= 1 && slot < inventory.PlayerSlots // 0 is the crafting output
	switch {
	case c.gamemode() != player.Creative:
		c.flag("inventory.creative", fmt.Sprintf("creative slot %d ← item %d ×%d in %v", slot, st.ID, st.Count, c.gamemode()))
	case slot == -1:
		return
	case !inRange:
		c.flag("badpackets.creativeslot", fmt.Sprintf("creative slot %d", slot))
		return
	case !st.Empty() && int(st.Count) > inventory.MaxStackFor(st.ID):
		c.flag("badpackets.creativeslot", fmt.Sprintf("item %d ×%d over its stack size", st.ID, st.Count))
	default:
		c.inv.Set(slot, st)
		return
	}
	if inRange {
		_ = c.sendSetSlot(0, slot, c.inv.Get(slot))
	}
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
	// GiveStack takes a byte count; feed it in stack-sized chunks.
	for remaining > 0 {
		chunk := min(remaining, inventory.MaxStack)
		left := c.GiveStack(inventory.Stack{ID: itemID, Count: byte(chunk)})
		remaining -= chunk - left
		if left > 0 {
			break
		}
	}
	return remaining
}

// GiveStack adds st (with all its display / enchantment data) to the
// inventory (inventory.Player.Give) and syncs every touched slot with its
// NBT. Returns the units that did not fit.
func (c *ClientConnection) GiveStack(st inventory.Stack) int {
	if st.Empty() {
		return 0
	}
	left, changed := c.inv.Give(st)
	c.syncSlots(changed)
	c.equipmentChanged()
	return left
}

// syncSlots re-sends window-0 slots from the model.
func (c *ClientConnection) syncSlots(slots []int16) {
	for _, slot := range slots {
		_ = c.sendSetSlot(0, slot, c.inv.Get(slot))
	}
}

// SetSlot overwrites one window-0 slot in the model and on the client. An
// empty stack clears it. Armour / held changes are broadcast as equipment.
func (c *ClientConnection) SetSlot(slot int16, st inventory.Stack) {
	if slot < 0 || int(slot) >= inventory.PlayerSlots {
		return
	}
	if st.Empty() {
		st = inventory.Stack{}
	}
	c.inv.Set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
	c.equipmentChanged()
}

// ClearInventory empties every slot (armour, main, hotbar, offhand, cursor)
// and re-sends the whole window so the client agrees.
func (c *ClientConnection) ClearInventory() {
	for slot := int16(0); slot < inventory.PlayerSlots; slot++ {
		c.inv.Set(slot, inventory.Stack{})
	}
	c.cursor = inventory.Stack{}
	_ = c.sendInventoryContents()
	c.equipmentChanged()
}

// inventorySnapshot returns a copy of every non-empty slot with its index.
func (c *ClientConnection) inventorySnapshot() []indexedStack {
	var out []indexedStack
	for slot := int16(0); slot < inventory.PlayerSlots; slot++ {
		if st := c.inv.Get(slot); !st.Empty() {
			out = append(out, indexedStack{slot: slot, stack: st})
		}
	}
	return out
}

// indexedStack pairs a stack with its window-0 slot.
type indexedStack struct {
	slot  int16
	stack inventory.Stack
}

// countItem sums the units of itemID across the main inventory + hotbar.
func (c *ClientConnection) countItem(itemID int32) int { return c.inv.Count(itemID) }

// TakeItem removes count units of itemID (inventory.Player.Take), syncing
// every changed slot. Removes nothing and returns false when the player
// holds fewer than count.
func (c *ClientConnection) TakeItem(itemID int32, count int) bool {
	ok, changed := c.inv.Take(itemID, count)
	c.syncSlots(changed)
	return ok
}

// sendSetSlot writes a single inventory slot on the client (Set Container
// Slot). windowID 0 is the player's own inventory; slot is a window-0 index.
func (c *ClientConnection) sendSetSlot(windowID byte, slot int16, st inventory.Stack) error {
	return c.safeWrite(packetid.CbPlaySetContainerSlot, inventory.SlotPayload(windowID, slot, st))
}

// consumeHeld removes one unit from the held stack after a successful
// placement (block, bed, item frame) and syncs the slot. Creative players
// keep their stack, like vanilla. Without this a survival player could place
// a block, break it, and come out one block richer each round.
func (c *ClientConnection) consumeHeld() {
	if c.gamemode() == player.Creative {
		return
	}
	slot := int16(inventory.HotbarStart) + int16(c.heldSlot.Load())
	if !c.inv.TakeOne(slot) {
		return
	}
	_ = c.sendSetSlot(0, slot, c.inv.Get(slot))
	c.equipmentChanged()
}

// heldItemName returns the namespaced id of the item in the selected hotbar
// slot, or "" if the slot is empty / the item id is unknown.
func (c *ClientConnection) heldItemName() string {
	st := c.inv.Held(c.heldSlot.Load())
	if st.Empty() {
		return ""
	}
	name, ok := world.ItemName(st.ID)
	if !ok {
		return ""
	}
	return name
}
