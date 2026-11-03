package server

import (
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/gameplay/chest"
	"minecraft-server/server/gameplay/inventory"
)

// clickInventory handles a Click Container on window 0.
func (c *ClientConnection) clickInventory(slot, button, mode int, claim inventory.Claim) {
	creative := c.gamemode() == player.Creative
	var (
		v      *inventory.View
		before []inventory.Stack
		ok     bool
	)
	// The whole click — copy out, simulate, copy back — runs under the
	// model's lock, so a pickup or a shop purchase on the tick goroutine
	// can't land in between and be overwritten (lost or duplicated).
	c.inv.Update(func(slots *[inventory.PlayerSlots]inventory.Stack, cursor *inventory.Stack) {
		v = &inventory.View{
			Slots:    append([]inventory.Stack(nil), slots[:]...),
			Cursor:   *cursor,
			Creative: creative,
		}
		before = append([]inventory.Stack(nil), v.Slots...)
		v.Click(slot, button, mode, &c.drag)
		if ok = c.dropsAllowed(v.Drops); !ok {
			return
		}
		copy(slots[:], v.Slots)
		*cursor = v.Cursor
	})
	if !ok {
		_ = c.sendInventoryContents()
		return
	}
	c.throwAll(v.Drops)
	if !claim.Matches(before, v) {
		_ = c.sendInventoryContents()
	}
	c.equipmentChanged()
}

// clickChest handles a Click Container on an open chest window. The chest
// is updated under its store's lock, so two players clicking the same chest
// can't both take the same stack.
func (c *ClientConnection) clickChest(store chest.Store, slot, button, mode int, claim inventory.Claim) {
	var (
		v      *inventory.View
		before []inventory.Stack
		ok     bool
	)
	creative := c.gamemode() == player.Creative
	// Chest lock, then the inventory's: the click is atomic against other
	// viewers of the chest and against the tick goroutine's pickups alike.
	store.Update(func(slots *chest.Slots) {
		c.inv.Update(func(inv *[inventory.PlayerSlots]inventory.Stack, cursor *inventory.Stack) {
			v = &inventory.View{
				Slots:    make([]inventory.Stack, 0, inventory.ChestSlots+36),
				Cursor:   *cursor,
				Offhand:  inv[inventory.OffhandSlot],
				Chest:    true,
				Creative: creative,
			}
			v.Slots = append(v.Slots, slots[:]...)
			v.Slots = append(v.Slots, inv[inventory.MainStart:inventory.HotbarStart+9]...)
			before = append([]inventory.Stack(nil), v.Slots...)
			v.Click(slot, button, mode, &c.drag)
			if ok = c.dropsAllowed(v.Drops) && !inventory.StoresUndroppable(before[:inventory.ChestSlots], v.Slots[:inventory.ChestSlots]); !ok {
				return
			}
			copy(slots[:], v.Slots[:inventory.ChestSlots])
			copy(inv[inventory.MainStart:inventory.HotbarStart+9], v.Slots[inventory.ChestSlots:])
			inv[inventory.OffhandSlot] = v.Offhand
			*cursor = v.Cursor
		})
	})
	if !ok {
		_ = c.sendChestInventory(store.Contents())
		return
	}
	c.throwAll(v.Drops)
	if !claim.Matches(before, v) {
		_ = c.sendChestInventory(store.Contents())
	}
	c.equipmentChanged()
}

// dropsAllowed refuses a click that would throw an undroppable item (NoDrop,
// hub UI items) or exceed the drop budget (anticheat.DropRate); the caller
// re-sends the window.
func (c *ClientConnection) dropsAllowed(drops []inventory.Stack) bool {
	for _, st := range drops {
		if st.Undroppable() {
			return false
		}
	}
	for range drops {
		if !c.ac.AllowDrop(time.Now()) {
			return false
		}
	}
	return true
}

// throwAll throws every dropped stack from the player's eyes.
func (c *ClientConnection) throwAll(drops []inventory.Stack) {
	for _, st := range drops {
		c.throwStack(st)
	}
}

// returnCraftGrid moves whatever sits in the 2×2 crafting grid back into the
// inventory when the inventory screen closes (vanilla InventoryMenu.removed);
// what doesn't fit is dropped — except an undroppable stack, which stays in
// the grid (like dropCursor keeps it on the cursor).
func (c *ClientConnection) returnCraftGrid() {
	for i := int16(inventory.CraftGridStart); i < inventory.ArmorStart; i++ {
		st := c.inv.Get(i)
		if st.Empty() {
			continue
		}
		c.inv.Set(i, inventory.Stack{})
		left := c.GiveStack(st)
		switch {
		case left == 0:
		case st.Undroppable():
			c.inv.Set(i, inventory.WithCount(st, left))
		default:
			c.throwStack(inventory.WithCount(st, left))
		}
	}
}

// closeWindows closes whatever the player has open the way closing it on
// the client would — the drag is dropped, the cursor stack thrown (or put
// back), the crafting grid emptied — and closes the server's menu / chest
// window on the client too. MovePlayer calls it: the Respawn closes the
// screen client-side without the client sending Close Container.
func (c *ClientConnection) closeWindows() {
	c.drag.Reset()
	c.dropCursor()
	c.returnCraftGrid()
	if c.menu.Swap(nil) != nil {
		_ = c.safeWrite(packetid.CbPlayCloseContainer, []byte{menuWindowID})
	}
}
