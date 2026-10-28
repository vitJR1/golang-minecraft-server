package server

import (
	"bytes"
	"fmt"
	"strings"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// click.go simulates Click Container on the server, the way vanilla's
// AbstractContainerMenu.doClick (1.20.1) does: the packet's slot / button /
// mode say what the player did, and the server works out the result on its
// own model. The client's "changed slots" + carried item are only its
// prediction — compared against the simulation, never stored. A mismatch
// (desync, or a forged result) gets the whole window re-sent. Because stacks
// only ever move between the server's own slots, nothing a client writes
// into a click — counts, items, enchantment NBT — can enter the inventory.
//
// Crafting isn't modelled: the 2×2 grid holds items like any slot, but the
// result slot stays empty (the client doesn't predict crafting results, the
// server sends them), so nothing can be crafted.

// Click Container modes (vanilla ClickType ordinals).
const (
	clickPickup     = 0
	clickQuickMove  = 1
	clickSwap       = 2
	clickClone      = 3
	clickThrow      = 4
	clickQuickCraft = 5
	clickPickupAll  = 6
)

const (
	slotOutside      = -999 // click outside the window
	offhandSwapKey   = 40   // SWAP button for the F key
	maxClaimedSlots  = 128  // vanilla's cap on the changed-slots array
	craftResultSlot  = 0    // window 0
	craftGridStart   = 1    // window 0, 2×2 grid 1..4
	armorStart       = 5    // window 0, helmet..boots 5..8
	offhandSlot      = 45   // window 0
	chestPlayerStart = chestSlotCount
)

// dragState is a drag-click (mode 5) in progress: start, slots added one
// packet at a time, end. Kept per connection between packets.
type dragState struct {
	status int // 0 idle, 1 collecting slots
	typ    int // 0 split evenly (left), 1 one each (right), 2 full stacks (middle, creative)
	slots  []int
}

func (d *dragState) reset() { *d = dragState{} }

// clickView is one open window as the simulation sees it: the window's slots
// in wire order plus the cursor. Window 0 is the player's 46 slots; a chest
// window is the 27 chest slots followed by the player's main inventory and
// hotbar (window 27+i = player slot 9+i). The off hand isn't part of a chest
// window but the F-key swap still reaches it, hence offhand.
type clickView struct {
	slots    []itemStack
	cursor   itemStack
	offhand  itemStack // chest windows only
	chest    bool
	creative bool
	drops    []itemStack
}

// hotbarSlot is the window index of hotbar key k (0..8).
func (v *clickView) hotbarSlot(k int) int {
	if v.chest {
		return chestPlayerStart + 27 + k
	}
	return hotbarStart + k
}

// playerRange is the window's player main inventory + hotbar range.
func (v *clickView) playerRange() (start, end int) {
	if v.chest {
		return chestPlayerStart, chestPlayerStart + 36
	}
	return mainInvStart, hotbarStart + 9
}

// valid reports whether i is a slot of this window.
func (v *clickView) valid(i int) bool { return i >= 0 && i < len(v.slots) }

// maxIn is how many of st slot i takes: the item's stack size, capped at one
// in an armor slot.
func (v *clickView) maxIn(i int, st itemStack) int {
	n := maxStackFor(st.ID)
	if !v.chest && i >= armorStart && i < armorStart+4 {
		n = 1
	}
	return n
}

// mayPlace is vanilla Slot.mayPlace: the crafting result takes nothing, an
// armor slot only its own piece.
func (v *clickView) mayPlace(i int, st itemStack) bool {
	if v.chest {
		return true
	}
	switch {
	case i == craftResultSlot:
		return false
	case i >= armorStart && i < armorStart+4:
		piece, ok := equipmentSlotOf(itemNameOf(st.ID))
		return ok && piece == i-armorStart
	}
	return true
}

// itemNameOf is the registry name of an item id ("" if unknown).
func itemNameOf(id int32) string {
	name, _ := world.ItemName(id)
	return name
}

// equipmentSlotOf maps an item to the armor slot it is worn in: 0 head,
// 1 chest, 2 legs, 3 feet (vanilla Mob.getEquipmentSlotForItem). Shields
// report 4, the off hand.
func equipmentSlotOf(name string) (int, bool) {
	n := strings.TrimPrefix(name, "minecraft:")
	switch {
	case strings.HasSuffix(n, "_helmet"), n == "carved_pumpkin",
		strings.HasSuffix(n, "_head"), strings.HasSuffix(n, "_skull"):
		return 0, true
	case strings.HasSuffix(n, "_chestplate"), n == "elytra":
		return 1, true
	case strings.HasSuffix(n, "_leggings"):
		return 2, true
	case strings.HasSuffix(n, "_boots"):
		return 3, true
	case n == "shield":
		return 4, true
	}
	return 0, false
}

// same is vanilla isSameItemSameTags: stacks that may merge.
func same(a, b itemStack) bool { return !a.empty() && !b.empty() && a.sameKind(b) }

// withCount returns st holding n (empty at 0).
func withCount(st itemStack, n int) itemStack {
	if n <= 0 {
		return itemStack{}
	}
	st.Count = byte(n)
	return st
}

// take removes up to n from slot i and returns what was removed.
func (v *clickView) take(i, n int) itemStack {
	st := v.slots[i]
	n = min(n, int(st.Count))
	if n <= 0 {
		return itemStack{}
	}
	v.slots[i] = withCount(st, int(st.Count)-n)
	return withCount(st, n)
}

// safeInsert is vanilla Slot.safeInsert: move up to n of st into slot i,
// returning what is left of st.
func (v *clickView) safeInsert(i int, st itemStack, n int) itemStack {
	if st.empty() || !v.mayPlace(i, st) {
		return st
	}
	cur := v.slots[i]
	n = min(n, int(st.Count), v.maxIn(i, st)-int(cur.Count))
	if n <= 0 {
		return st
	}
	switch {
	case cur.empty():
		v.slots[i] = withCount(st, n)
	case same(cur, st):
		v.slots[i] = withCount(cur, int(cur.Count)+n)
	default:
		return st
	}
	return withCount(st, int(st.Count)-n)
}

// quickReplaceable is vanilla canItemQuickReplace(slot, stack, true).
func (v *clickView) quickReplaceable(i int, st itemStack) bool {
	cur := v.slots[i]
	if cur.empty() {
		return true
	}
	return same(cur, st) && int(cur.Count) <= maxStackFor(st.ID)
}

// drop queues st to be thrown from the player.
func (v *clickView) drop(st itemStack) {
	if !st.empty() {
		v.drops = append(v.drops, st)
	}
}

// click runs one Click Container. drag carries a drag-click across packets.
func (v *clickView) click(slot, button, mode int, drag *dragState) {
	if mode == clickQuickCraft {
		v.quickCraft(slot, button, drag)
		return
	}
	if drag.status != 0 {
		// Any other click cancels a drag in progress, and does nothing else.
		drag.reset()
		return
	}
	switch mode {
	case clickPickup, clickQuickMove:
		if button != 0 && button != 1 {
			return
		}
		switch {
		case slot == slotOutside:
			if !v.cursor.empty() {
				if button == 0 {
					v.drop(v.cursor)
					v.cursor = itemStack{}
				} else {
					v.drop(withCount(v.cursor, 1))
					v.cursor = withCount(v.cursor, int(v.cursor.Count)-1)
				}
			}
		case !v.valid(slot):
		case mode == clickQuickMove:
			// Repeat while part of the stack moved and the rest is still the
			// same item (vanilla's loop); quickMove reports no progress as false.
			for v.quickMove(slot) && !v.slots[slot].empty() {
			}
		default:
			v.pickup(slot, button)
		}
	case clickSwap:
		if v.valid(slot) && (button >= 0 && button < 9 || button == offhandSwapKey) {
			v.swap(slot, button)
		}
	case clickClone:
		if v.creative && v.cursor.empty() && v.valid(slot) && !v.slots[slot].empty() {
			st := v.slots[slot]
			v.cursor = withCount(st, maxStackFor(st.ID))
		}
	case clickThrow:
		if v.cursor.empty() && v.valid(slot) && !v.slots[slot].empty() {
			n := 1
			if button != 0 {
				n = int(v.slots[slot].Count)
			}
			v.drop(v.take(slot, n))
		}
	case clickPickupAll:
		if v.valid(slot) {
			v.pickupAll(slot, button)
		}
	}
}

// pickup is a plain left (button 0) / right (1) click on a slot.
func (v *clickView) pickup(i, button int) {
	cur, carried := v.slots[i], v.cursor
	switch {
	case cur.empty():
		if !carried.empty() {
			n := int(carried.Count)
			if button == 1 {
				n = 1
			}
			v.cursor = v.safeInsert(i, carried, n)
		}
	case carried.empty():
		n := int(cur.Count)
		if button == 1 {
			n = (n + 1) / 2
		}
		v.cursor = v.take(i, n)
	case v.mayPlace(i, carried):
		if same(cur, carried) {
			n := int(carried.Count)
			if button == 1 {
				n = 1
			}
			v.cursor = v.safeInsert(i, carried, n)
		} else if int(carried.Count) <= v.maxIn(i, carried) {
			v.slots[i], v.cursor = carried, cur
		}
	case same(cur, carried):
		// A slot that takes nothing (crafting result) still gives.
		got := v.take(i, maxStackFor(carried.ID)-int(carried.Count))
		v.cursor = withCount(carried, int(carried.Count)+int(got.Count))
	}
}

// swap is the number-key / F swap between slot i and a hotbar slot or the
// off hand.
func (v *clickView) swap(i, button int) {
	var hot *itemStack
	switch {
	case button == offhandSwapKey && v.chest:
		hot = &v.offhand
	case button == offhandSwapKey:
		hot = &v.slots[offhandSlot]
	default:
		hot = &v.slots[v.hotbarSlot(button)]
	}
	h, cur := *hot, v.slots[i]
	switch {
	case h.empty() && cur.empty():
	case h.empty():
		*hot = cur
		v.slots[i] = itemStack{}
	case cur.empty():
		if !v.mayPlace(i, h) {
			return
		}
		if limit := v.maxIn(i, h); int(h.Count) > limit {
			v.slots[i] = withCount(h, limit)
			*hot = withCount(h, int(h.Count)-limit)
		} else {
			*hot = itemStack{}
			v.slots[i] = h
		}
	case v.mayPlace(i, h):
		if limit := v.maxIn(i, h); int(h.Count) > limit {
			v.slots[i] = withCount(h, limit)
			*hot = withCount(h, int(h.Count)-limit)
			v.addToPlayer(cur)
		} else {
			*hot, v.slots[i] = cur, h
		}
	}
}

// addToPlayer is vanilla Inventory.add for the window's player slots: top
// up matching stacks, then the first free slot (hotbar first); the rest is
// dropped.
func (v *clickView) addToPlayer(st itemStack) {
	start, end := v.playerRange()
	order := make([]int, 0, 36)
	for k := 0; k < 9; k++ {
		order = append(order, v.hotbarSlot(k))
	}
	for i := start; i < end; i++ {
		if i < v.hotbarSlot(0) || i > v.hotbarSlot(8) {
			order = append(order, i)
		}
	}
	for _, i := range order {
		if same(v.slots[i], st) {
			st = v.safeInsert(i, st, int(st.Count))
		}
	}
	for _, i := range order {
		if v.slots[i].empty() {
			st = v.safeInsert(i, st, int(st.Count))
		}
	}
	v.drop(st)
}

// pickupAll is the double click: gather the cursor's item from every slot,
// non-full stacks on the first pass, full ones on the second.
func (v *clickView) pickupAll(i, button int) {
	carried := v.cursor
	if carried.empty() || !v.slots[i].empty() {
		return
	}
	first, step := 0, 1
	if button != 0 {
		first, step = len(v.slots)-1, -1
	}
	limit := maxStackFor(carried.ID)
	for pass := 0; pass < 2; pass++ {
		for k := first; k >= 0 && k < len(v.slots) && int(carried.Count) < limit; k += step {
			st := v.slots[k]
			if st.empty() || !same(st, carried) || (!v.chest && k == craftResultSlot) {
				continue
			}
			if pass == 0 && int(st.Count) == maxStackFor(st.ID) {
				continue
			}
			got := v.take(k, limit-int(carried.Count))
			carried = withCount(carried, int(carried.Count)+int(got.Count))
		}
	}
	v.cursor = carried
}

// quickCraft advances a drag-click: button = header (0 start, 1 add slot,
// 2 end) | type<<2.
func (v *clickView) quickCraft(slot, button int, d *dragState) {
	prev := d.status
	header, typ := button&3, (button>>2)&3
	d.status = header
	switch {
	case (prev != 1 || header != 2) && prev != header:
		d.reset()
	case v.cursor.empty():
		d.reset()
	case header == 0:
		if typ == 0 || typ == 1 || (typ == 2 && v.creative) {
			d.status, d.typ, d.slots = 1, typ, nil
		} else {
			d.reset()
		}
	case header == 1:
		if !v.valid(slot) {
			return
		}
		for _, s := range d.slots {
			if s == slot {
				return // a set: each slot once
			}
		}
		if v.quickReplaceable(slot, v.cursor) && v.mayPlace(slot, v.cursor) &&
			(d.typ == 2 || int(v.cursor.Count) > len(d.slots)) {
			d.slots = append(d.slots, slot)
		}
	case header == 2:
		slots, typ := d.slots, d.typ
		d.reset()
		if len(slots) == 1 {
			// One slot: an ordinary click with the drag's button.
			v.click(slots[0], typ, clickPickup, d)
			return
		}
		if len(slots) == 0 {
			return
		}
		carried := v.cursor
		left := int(carried.Count)
		for _, s := range slots {
			if !v.quickReplaceable(s, carried) || !v.mayPlace(s, carried) ||
				(typ != 2 && int(carried.Count) < len(slots)) {
				continue
			}
			have := int(v.slots[s].Count)
			limit := min(maxStackFor(carried.ID), v.maxIn(s, carried))
			per := 1
			switch typ {
			case 0:
				per = int(carried.Count) / len(slots)
			case 2:
				per = maxStackFor(carried.ID)
			}
			n := min(per+have, limit)
			left -= n - have
			v.slots[s] = withCount(carried, n)
		}
		v.cursor = withCount(carried, left)
	default:
		d.reset()
	}
}

// quickMove is vanilla quickMoveStack for one shift-click on slot i: true
// when something moved.
func (v *clickView) quickMove(i int) bool {
	st := v.slots[i]
	if st.empty() {
		return false
	}
	before := st.Count
	if v.chest {
		if i < chestSlotCount {
			st = v.moveTo(st, chestPlayerStart, len(v.slots), true)
		} else {
			st = v.moveTo(st, 0, chestSlotCount, false)
		}
	} else {
		piece, worn := equipmentSlotOf(itemNameOf(st.ID))
		switch {
		case i == craftResultSlot:
			return false // never holds anything
		case i < armorStart: // crafting grid
			st = v.moveTo(st, mainInvStart, offhandSlot, false)
		case i < mainInvStart: // armor
			st = v.moveTo(st, mainInvStart, offhandSlot, false)
		case worn && piece < 4 && v.slots[armorStart+piece].empty():
			st = v.moveTo(st, armorStart+piece, armorStart+piece+1, false)
		case worn && piece == 4 && v.slots[offhandSlot].empty():
			st = v.moveTo(st, offhandSlot, offhandSlot+1, false)
		case i < hotbarStart:
			st = v.moveTo(st, hotbarStart, hotbarStart+9, false)
		case i < hotbarStart+9:
			st = v.moveTo(st, mainInvStart, hotbarStart, false)
		default: // off hand
			st = v.moveTo(st, mainInvStart, offhandSlot, false)
		}
	}
	v.slots[i] = st
	return st.Count != before
}

// moveTo is vanilla moveItemStackTo: merge st into matching stacks of
// [start, end), then into the first empty slot; returns what is left.
func (v *clickView) moveTo(st itemStack, start, end int, reverse bool) itemStack {
	next := func(i int) int {
		if reverse {
			return i - 1
		}
		return i + 1
	}
	first := start
	if reverse {
		first = end - 1
	}
	if maxStackFor(st.ID) > 1 {
		for i := first; !st.empty() && i >= start && i < end; i = next(i) {
			cur := v.slots[i]
			if !same(cur, st) {
				continue
			}
			limit := min(v.maxIn(i, st), maxStackFor(st.ID))
			if total := int(cur.Count) + int(st.Count); total <= limit {
				v.slots[i] = withCount(cur, total)
				st = itemStack{}
			} else if int(cur.Count) < limit {
				st = withCount(st, total-limit)
				v.slots[i] = withCount(cur, limit)
			}
		}
	}
	if !st.empty() {
		for i := first; i >= start && i < end; i = next(i) {
			if v.slots[i].empty() && v.mayPlace(i, st) {
				n := min(int(st.Count), v.maxIn(i, st))
				v.slots[i] = withCount(st, n)
				st = withCount(st, int(st.Count)-n)
				break
			}
		}
	}
	return st
}

// clickClaim is the client's prediction from a Click Container: the slots it
// says changed and the item it says it carries afterwards.
type clickClaim struct {
	slots  map[int]itemStack
	cursor itemStack
}

// readClickClaim parses the changed-slots array + carried item.
func readClickClaim(packet *bytes.Buffer) (clickClaim, error) {
	n, err := protocol.ReadVarInt(packet)
	if err != nil {
		return clickClaim{}, fmt.Errorf("changed slot count: %w", err)
	}
	if n < 0 || n > maxClaimedSlots {
		return clickClaim{}, fmt.Errorf("%d changed slots", n)
	}
	claim := clickClaim{slots: make(map[int]itemStack, n)}
	for k := 0; k < n; k++ {
		raw, err := protocol.ReadUShortFromBuf(packet)
		if err != nil {
			return clickClaim{}, fmt.Errorf("changed slot index: %w", err)
		}
		st, ok := readSlot(packet)
		if !ok {
			return clickClaim{}, fmt.Errorf("changed slot %d item", int16(raw))
		}
		claim.slots[int(int16(raw))] = st
	}
	cursor, ok := readSlot(packet)
	if !ok {
		return clickClaim{}, fmt.Errorf("carried item")
	}
	claim.cursor = cursor
	return claim, nil
}

// stackEqual: same count and, when non-empty, the same item data.
func stackEqual(a, b itemStack) bool {
	return a.Count == b.Count && (a.empty() || a.sameKind(b))
}

// matches reports whether the client predicted exactly the simulated result:
// every slot it reported holds what it says, every slot that changed was
// reported, and the cursor agrees.
func (c clickClaim) matches(before []itemStack, v *clickView) bool {
	if !stackEqual(c.cursor, v.cursor) {
		return false
	}
	for i, st := range c.slots {
		if !v.valid(i) || !stackEqual(st, v.slots[i]) {
			return false
		}
	}
	for i := range v.slots {
		if _, reported := c.slots[i]; !reported && !stackEqual(before[i], v.slots[i]) {
			return false
		}
	}
	return true
}

// clickInventory handles a Click Container on window 0.
func (c *ClientConnection) clickInventory(slot, button, mode int, claim clickClaim) {
	v := &clickView{
		slots:    append([]itemStack(nil), c.inv.slots[:]...),
		cursor:   c.cursor,
		creative: c.gamemode() == player.Creative,
	}
	before := append([]itemStack(nil), v.slots...)
	v.click(slot, button, mode, &c.drag)
	if !c.dropsAllowed(v.drops) {
		_ = c.sendInventoryContents()
		return
	}
	copy(c.inv.slots[:], v.slots)
	c.cursor = v.cursor
	c.throwAll(v.drops)
	if !claim.matches(before, v) {
		_ = c.sendInventoryContents()
	}
	c.equipmentChanged()
}

// clickChest handles a Click Container on an open chest window. The chest
// is updated under its store's lock, so two players clicking the same chest
// can't both take the same stack.
func (c *ClientConnection) clickChest(store chestStore, slot, button, mode int, claim clickClaim) {
	var (
		v      *clickView
		before []itemStack
		ok     bool
	)
	store.update(func(chest *[chestSlotCount]itemStack) {
		v = &clickView{
			slots:    make([]itemStack, 0, chestSlotCount+36),
			cursor:   c.cursor,
			offhand:  c.inv.get(offhandSlot),
			chest:    true,
			creative: c.gamemode() == player.Creative,
		}
		v.slots = append(v.slots, chest[:]...)
		v.slots = append(v.slots, c.inv.slots[mainInvStart:hotbarStart+9]...)
		before = append([]itemStack(nil), v.slots...)
		v.click(slot, button, mode, &c.drag)
		if ok = c.dropsAllowed(v.drops); !ok {
			return
		}
		copy(chest[:], v.slots[:chestSlotCount])
	})
	if !ok {
		_ = c.sendChestInventory(store.contents())
		return
	}
	copy(c.inv.slots[mainInvStart:hotbarStart+9], v.slots[chestSlotCount:])
	c.inv.set(offhandSlot, v.offhand)
	c.cursor = v.cursor
	c.throwAll(v.drops)
	if !claim.matches(before, v) {
		_ = c.sendChestInventory(store.contents())
	}
	c.equipmentChanged()
}

// dropsAllowed refuses a click that would throw a hub UI item (navigator,
// arena selector) — those never leave the inventory.
func (c *ClientConnection) dropsAllowed(drops []itemStack) bool {
	for _, st := range drops {
		if st.Name == navigatorName || st.Name == selectorName {
			return false
		}
	}
	return true
}

// throwAll throws every dropped stack from the player's eyes.
func (c *ClientConnection) throwAll(drops []itemStack) {
	for _, st := range drops {
		c.throwStack(st)
	}
}

// returnCraftGrid moves whatever sits in the 2×2 crafting grid back into the
// inventory when the inventory screen closes (vanilla InventoryMenu.removed);
// what doesn't fit is dropped.
func (c *ClientConnection) returnCraftGrid() {
	for i := int16(craftGridStart); i < armorStart; i++ {
		st := c.inv.get(i)
		if st.empty() {
			continue
		}
		c.inv.set(i, itemStack{})
		if left := c.giveStack(st); left > 0 {
			c.throwStack(withCount(st, left))
		}
	}
}
