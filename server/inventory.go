package server

import (
	"bytes"
	"minecraft-server/player"
	"strings"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

// maxStackSize is the vanilla stack ceiling for plain items (ingots,
// blocks, …). maxStackFor narrows it for the items that stack to 16 or 1.
const maxStackSize = 64

// maxStackFor returns the vanilla max stack size of an item: 1 for tools,
// weapons, armour, bows, shears, buckets and potions; 16 for ender pearls,
// eggs, snowballs and signs; 64 otherwise.
func maxStackFor(itemID int32) int {
	name, ok := world.ItemName(itemID)
	if !ok {
		return maxStackSize
	}
	short := strings.TrimPrefix(name, "minecraft:")
	switch short {
	case "ender_pearl", "egg", "snowball", "oak_sign", "armor_stand", "honey_bottle":
		return 16
	case "bow", "crossbow", "shears", "shield", "fishing_rod", "flint_and_steel", "potion",
		"splash_potion", "lingering_potion", "milk_bucket", "bucket", "water_bucket", "lava_bucket",
		"elytra", "trident", "totem_of_undying", "saddle", "enchanted_book", "written_book":
		return 1
	}
	if world.ToolDurability(name) > 0 {
		return 1 // every damageable item (tools, weapons, armour) is unstackable
	}
	return maxStackSize
}

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

// withKnownName re-attaches the custom display name of a server-given UI item
// to a stack the client reported back (the wire reader drops item NBT, so a
// moved "Navigator" rod would otherwise come back nameless and stop opening
// the menu). Only IDs that currently carry a name in the inventory qualify.
func (inv *playerInventory) withKnownName(st itemStack) itemStack {
	if st.empty() || st.Name != "" {
		return st
	}
	for _, cur := range inv.slots {
		if cur.ID == st.ID && !cur.empty() && cur.Name != "" {
			st.Name = cur.Name
			break
		}
	}
	return st
}

// writeInventoryMirror appends the 36 player-inventory slots (main 9..35 then
// hotbar 36..44) of a container window, from the server-side model, so an
// open chest/menu shows the player's real items instead of blanks.
func (c *ClientConnection) writeInventoryMirror(buf *bytes.Buffer) {
	for slot := int16(mainInvStart); slot < hotbarStart+9; slot++ {
		writeStack(buf, c.inv.get(slot))
	}
}

// sendInventoryContents pushes the whole window-0 inventory (Set Container
// Content) from the model: after a Respawn, a cross-instance move, or a
// closed menu window, so the client's view matches the server again.
func (c *ClientConnection) sendInventoryContents() error {
	var buf bytes.Buffer
	buf.WriteByte(0)                        // window 0 = player inventory
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	protocol.WriteVarInt32ToBuffer(&buf, int32(playerInvSize))
	for slot := int16(0); slot < playerInvSize; slot++ {
		writeStack(&buf, c.inv.get(slot))
	}
	buf.Write(protocol.WriteEmptySlot()) // cursor
	return c.safeWrite(CbPlaySetContainerContent, buf.Bytes())
}

// applyInventoryClick applies a window-0 Click Container (the player moving
// stacks around their own inventory: pick up, place, merge, shift-click) to
// the model by trusting the client's changed-slots array, exactly like chest
// windows. packet is positioned after the window/state/slot/button/mode
// header. Without this the model drifts from the client as soon as a stack
// is moved, and the next server-side give would overwrite the wrong slot.
//
// Returns the carried (cursor) stack the client reports after the click.
func (c *ClientConnection) applyInventoryClick(packet *bytes.Buffer) itemStack {
	count, err := protocol.ReadVarInt(packet)
	if err != nil || count < 0 {
		return c.cursor
	}
	for n := 0; n < count; n++ {
		raw, err := protocol.ReadUShortFromBuf(packet)
		if err != nil {
			return c.cursor
		}
		st, ok := readSlot(packet)
		if !ok {
			return c.cursor
		}
		c.inv.set(int16(raw), c.inv.withKnownName(st))
	}
	cursor, ok := readSlot(packet)
	if !ok {
		return c.cursor
	}
	c.equipmentChanged()
	return cursor
}

// stripNavigatorItems removes the server-given hub UI items (navigator rod,
// arena selector) from the inventory. Called when the player enters a game
// instance, where those slots belong to the game's economy.
func (c *ClientConnection) stripNavigatorItems() {
	for slot := int16(mainInvStart); slot < hotbarStart+9; slot++ {
		st := c.inv.get(slot)
		if st.Name == navigatorName || st.Name == selectorName {
			c.inv.set(slot, itemStack{})
		}
	}
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
	// giveStack takes a byte count; feed it in stack-sized chunks.
	for remaining > 0 {
		chunk := min(remaining, maxStackSize)
		left := c.giveStack(itemStack{ID: itemID, Count: byte(chunk)})
		remaining -= chunk - left
		if left > 0 {
			break
		}
	}
	return remaining
}

// giveStack adds st (with all its display / enchantment data) to the
// inventory: first topping up stacks of the same kind, then filling empty
// slots hotbar-first, like giveItem. Stack limits follow maxStackFor. Every
// touched slot is synced with its NBT. Returns the units that did not fit.
func (c *ClientConnection) giveStack(st itemStack) int {
	if st.empty() {
		return 0
	}
	remaining := int(st.Count)
	limit := maxStackFor(st.ID)
	for pass := 0; pass < 2 && remaining > 0; pass++ {
		for _, slot := range giveSlotOrder {
			if remaining == 0 {
				break
			}
			cur := c.inv.get(slot)
			if pass == 0 {
				if cur.empty() || !cur.sameKind(st) || int(cur.Count) >= limit {
					continue
				}
			} else {
				if !cur.empty() {
					continue
				}
				cur = st
				cur.Count = 0
			}
			add := min(limit-int(cur.Count), remaining)
			cur.Count += byte(add)
			remaining -= add
			c.inv.set(slot, cur)
			_ = c.sendSetSlot(0, slot, cur)
		}
	}
	c.equipmentChanged()
	return remaining
}

// setSlot overwrites one window-0 slot in the model and on the client. An
// empty stack clears it. Armour / held changes are broadcast as equipment.
func (c *ClientConnection) setSlot(slot int16, st itemStack) {
	if slot < 0 || int(slot) >= playerInvSize {
		return
	}
	if st.empty() {
		st = itemStack{}
	}
	c.inv.set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
	c.equipmentChanged()
}

// clearInventory empties every slot (armour, main, hotbar, offhand, cursor)
// and re-sends the whole window so the client agrees.
func (c *ClientConnection) clearInventory() {
	for slot := int16(0); slot < playerInvSize; slot++ {
		c.inv.set(slot, itemStack{})
	}
	c.cursor = itemStack{}
	_ = c.sendInventoryContents()
	c.equipmentChanged()
}

// inventorySnapshot returns a copy of every non-empty slot with its index.
func (c *ClientConnection) inventorySnapshot() []indexedStack {
	var out []indexedStack
	for slot := int16(0); slot < playerInvSize; slot++ {
		if st := c.inv.get(slot); !st.empty() {
			out = append(out, indexedStack{slot: slot, stack: st})
		}
	}
	return out
}

// indexedStack pairs a stack with its window-0 slot.
type indexedStack struct {
	slot  int16
	stack itemStack
}

// countItem sums the units of itemID across the main inventory + hotbar.
func (c *ClientConnection) countItem(itemID int32) int {
	n := 0
	for _, slot := range giveSlotOrder {
		if st := c.inv.get(slot); !st.empty() && st.ID == itemID {
			n += int(st.Count)
		}
	}
	return n
}

// takeItem removes count units of itemID, main inventory first so the
// hotbar keeps its stacks longest, syncing every changed slot. Removes
// nothing and returns false when the player holds fewer than count.
func (c *ClientConnection) takeItem(itemID int32, count int) bool {
	if count <= 0 {
		return true
	}
	if c.countItem(itemID) < count {
		return false
	}
	remaining := count
	for idx := len(giveSlotOrder) - 1; idx >= 0 && remaining > 0; idx-- {
		slot := giveSlotOrder[idx]
		st := c.inv.get(slot)
		if st.empty() || st.ID != itemID {
			continue
		}
		take := min(int(st.Count), remaining)
		st.Count -= byte(take)
		remaining -= take
		if st.Count == 0 {
			st = itemStack{}
		}
		c.inv.set(slot, st)
		_ = c.sendSetSlot(0, slot, st)
	}
	return true
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
	writeStack(&buf, st) // keeps name / lore / enchantments / damage
	return c.safeWrite(CbPlaySetContainerSlot, buf.Bytes())
}

// consumeHeld removes one unit from the held stack after a successful
// placement (block, bed, item frame) and syncs the slot. Creative players
// keep their stack, like vanilla. Without this a survival player could place
// a block, break it, and come out one block richer each round.
func (c *ClientConnection) consumeHeld() {
	if c.gamemode() == player.Creative {
		return
	}
	slot := int16(hotbarStart) + int16(c.heldSlot.Load())
	st := c.inv.get(slot)
	if st.empty() {
		return
	}
	st.Count--
	if st.Count == 0 {
		st = itemStack{}
	}
	c.inv.set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
	c.equipmentChanged()
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
