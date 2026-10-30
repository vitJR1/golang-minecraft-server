package server

import (
	"time"

	"minecraft-server/player"
	"minecraft-server/server/chest"
	"minecraft-server/server/inventory"
)

// clickInventory handles a Click Container on window 0.
func (c *ClientConnection) clickInventory(slot, button, mode int, claim inventory.Claim) {
	v := &inventory.View{
		Slots:    append([]inventory.Stack(nil), c.inv.Slots[:]...),
		Cursor:   c.cursor,
		Creative: c.gamemode() == player.Creative,
	}
	before := append([]inventory.Stack(nil), v.Slots...)
	v.Click(slot, button, mode, &c.drag)
	if !c.dropsAllowed(v.Drops) {
		_ = c.sendInventoryContents()
		return
	}
	copy(c.inv.Slots[:], v.Slots)
	c.cursor = v.Cursor
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
	store.Update(func(slots *chest.Slots) {
		v = &inventory.View{
			Slots:    make([]inventory.Stack, 0, inventory.ChestSlots+36),
			Cursor:   c.cursor,
			Offhand:  c.inv.Get(inventory.OffhandSlot),
			Chest:    true,
			Creative: c.gamemode() == player.Creative,
		}
		v.Slots = append(v.Slots, slots[:]...)
		v.Slots = append(v.Slots, c.inv.Slots[inventory.MainStart:inventory.HotbarStart+9]...)
		before = append([]inventory.Stack(nil), v.Slots...)
		v.Click(slot, button, mode, &c.drag)
		if ok = c.dropsAllowed(v.Drops) && !inventory.StoresUndroppable(before[:inventory.ChestSlots], v.Slots[:inventory.ChestSlots]); !ok {
			return
		}
		copy(slots[:], v.Slots[:inventory.ChestSlots])
	})
	if !ok {
		_ = c.sendChestInventory(store.Contents())
		return
	}
	copy(c.inv.Slots[inventory.MainStart:inventory.HotbarStart+9], v.Slots[inventory.ChestSlots:])
	c.inv.Set(inventory.OffhandSlot, v.Offhand)
	c.cursor = v.Cursor
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
// what doesn't fit is dropped.
func (c *ClientConnection) returnCraftGrid() {
	for i := int16(inventory.CraftGridStart); i < inventory.ArmorStart; i++ {
		st := c.inv.Get(i)
		if st.Empty() {
			continue
		}
		c.inv.Set(i, inventory.Stack{})
		if left := c.giveStack(st); left > 0 {
			c.throwStack(inventory.WithCount(st, left))
		}
	}
}
