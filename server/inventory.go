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

// writeInventoryMirror appends the 36 player-inventory slots (main 9..35 then
// hotbar 36..44) of a container window, from the server-side model, so an
// open chest/menu shows the player's real items instead of blanks.
func (c *ClientConnection) writeInventoryMirror(buf *bytes.Buffer) {
	for slot := int16(inventory.MainStart); slot < inventory.HotbarStart+9; slot++ {
		inventory.WriteSlot(buf, c.inv.Get(slot))
	}
}

// sendInventoryContents pushes the whole window-0 inventory (Set Container
// Content) from the model: after a Respawn, a cross-instance move, or a
// closed menu window, so the client's view matches the server again.
func (c *ClientConnection) sendInventoryContents() error {
	var buf bytes.Buffer
	buf.WriteByte(0)                        // window 0 = player inventory
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	protocol.WriteVarInt32ToBuffer(&buf, int32(inventory.PlayerSlots))
	for slot := int16(0); slot < inventory.PlayerSlots; slot++ {
		inventory.WriteSlot(&buf, c.inv.Get(slot))
	}
	inventory.WriteSlot(&buf, c.cursor)
	return c.safeWrite(packetid.CbPlaySetContainerContent, buf.Bytes())
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
	// giveStack takes a byte count; feed it in stack-sized chunks.
	for remaining > 0 {
		chunk := min(remaining, inventory.MaxStack)
		left := c.giveStack(inventory.Stack{ID: itemID, Count: byte(chunk)})
		remaining -= chunk - left
		if left > 0 {
			break
		}
	}
	return remaining
}

// giveStack adds st (with all its display / enchantment data) to the
// inventory: first topping up stacks of the same kind, then filling empty
// slots hotbar-first, like giveItem. Stack limits follow inventory.MaxStackFor. Every
// touched slot is synced with its NBT. Returns the units that did not fit.
func (c *ClientConnection) giveStack(st inventory.Stack) int {
	if st.Empty() {
		return 0
	}
	remaining := int(st.Count)
	limit := inventory.MaxStackFor(st.ID)
	for pass := 0; pass < 2 && remaining > 0; pass++ {
		for _, slot := range inventory.GiveOrder {
			if remaining == 0 {
				break
			}
			cur := c.inv.Get(slot)
			if pass == 0 {
				if cur.Empty() || !cur.SameKind(st) || int(cur.Count) >= limit {
					continue
				}
			} else {
				if !cur.Empty() {
					continue
				}
				cur = st
				cur.Count = 0
			}
			add := min(limit-int(cur.Count), remaining)
			cur.Count += byte(add)
			remaining -= add
			c.inv.Set(slot, cur)
			_ = c.sendSetSlot(0, slot, cur)
		}
	}
	c.equipmentChanged()
	return remaining
}

// setSlot overwrites one window-0 slot in the model and on the client. An
// empty stack clears it. Armour / held changes are broadcast as equipment.
func (c *ClientConnection) setSlot(slot int16, st inventory.Stack) {
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

// clearInventory empties every slot (armour, main, hotbar, offhand, cursor)
// and re-sends the whole window so the client agrees.
func (c *ClientConnection) clearInventory() {
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
func (c *ClientConnection) countItem(itemID int32) int {
	n := 0
	for _, slot := range inventory.GiveOrder {
		if st := c.inv.Get(slot); !st.Empty() && st.ID == itemID {
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
	for idx := len(inventory.GiveOrder) - 1; idx >= 0 && remaining > 0; idx-- {
		slot := inventory.GiveOrder[idx]
		st := c.inv.Get(slot)
		if st.Empty() || st.ID != itemID {
			continue
		}
		take := min(int(st.Count), remaining)
		st.Count -= byte(take)
		remaining -= take
		if st.Count == 0 {
			st = inventory.Stack{}
		}
		c.inv.Set(slot, st)
		_ = c.sendSetSlot(0, slot, st)
	}
	return true
}

// sendSetSlot writes a single inventory slot on the client (Set Container
// Slot). windowID 0 is the player's own inventory; slot is a window-0 index.
func (c *ClientConnection) sendSetSlot(windowID byte, slot int16, st inventory.Stack) error {
	var buf bytes.Buffer
	buf.WriteByte(windowID)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	buf.Write(protocol.WriteShort(slot))
	inventory.WriteSlot(&buf, st) // keeps name / lore / enchantments / damage
	return c.safeWrite(packetid.CbPlaySetContainerSlot, buf.Bytes())
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
	st := c.inv.Get(slot)
	if st.Empty() {
		return
	}
	st.Count--
	if st.Count == 0 {
		st = inventory.Stack{}
	}
	c.inv.Set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
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
