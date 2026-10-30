package inventory

import (
	"bytes"
	"fmt"
	"strings"

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
	ClickPickup     = 0
	ClickQuickMove  = 1
	ClickSwap       = 2
	ClickClone      = 3
	ClickThrow      = 4
	ClickQuickCraft = 5
	ClickPickupAll  = 6
)

const (
	SlotOutside      = -999 // click outside the window
	OffhandSwapKey   = 40   // SWAP button for the F key
	maxClaimedSlots  = 128  // vanilla's cap on the changed-slots array
	CraftResultSlot  = 0    // window 0
	CraftGridStart   = 1    // window 0, 2×2 grid 1..4
	ArmorStart       = 5    // window 0, helmet..boots 5..8
	HelmetSlot       = ArmorStart
	ChestplateSlot   = ArmorStart + 1
	LeggingsSlot     = ArmorStart + 2
	BootsSlot        = ArmorStart + 3
	OffhandSlot      = 45 // window 0
	chestPlayerStart = ChestSlots
)

// Drag is a drag-click (mode 5) in progress: start, slots added one
// packet at a time, end. Kept per connection between packets.
type Drag struct {
	status int // 0 idle, 1 collecting slots
	typ    int // 0 split evenly (left), 1 one each (right), 2 full stacks (middle, creative)
	Slots  []int
}

func (d *Drag) Reset() { *d = Drag{} }

// View is one open window as the simulation sees it: the window's slots
// in wire order plus the cursor. Window 0 is the player's 46 slots; a chest
// window is the 27 chest slots followed by the player's main inventory and
// hotbar (window 27+i = player slot 9+i). The off hand isn't part of a chest
// window but the F-key swap still reaches it, hence offhand.
type View struct {
	Slots    []Stack
	Cursor   Stack
	Offhand  Stack // chest windows only
	Chest    bool
	Creative bool
	Drops    []Stack
}

// hotbarSlot is the window index of hotbar key k (0..8).
func (v *View) hotbarSlot(k int) int {
	if v.Chest {
		return chestPlayerStart + 27 + k
	}
	return HotbarStart + k
}

// playerRange is the window's player main inventory + hotbar range.
func (v *View) playerRange() (start, end int) {
	if v.Chest {
		return chestPlayerStart, chestPlayerStart + 36
	}
	return MainStart, HotbarStart + 9
}

// valid reports whether i is a slot of this window.
func (v *View) valid(i int) bool { return i >= 0 && i < len(v.Slots) }

// maxIn is how many of st slot i takes: the item's stack size, capped at one
// in an armor slot.
func (v *View) maxIn(i int, st Stack) int {
	n := MaxStackFor(st.ID)
	if !v.Chest && i >= ArmorStart && i < ArmorStart+4 {
		n = 1
	}
	return n
}

// mayPlace is vanilla Slot.mayPlace: the crafting result takes nothing, an
// armor slot only its own piece.
func (v *View) mayPlace(i int, st Stack) bool {
	if v.Chest {
		return true
	}
	switch {
	case i == CraftResultSlot:
		return false
	case i >= ArmorStart && i < ArmorStart+4:
		piece, ok := equipmentSlotOf(itemNameOf(st.ID))
		return ok && piece == i-ArmorStart
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
func same(a, b Stack) bool { return !a.Empty() && !b.Empty() && a.SameKind(b) }

// WithCount returns st holding n (empty at 0).
func WithCount(st Stack, n int) Stack {
	if n <= 0 {
		return Stack{}
	}
	st.Count = byte(n)
	return st
}

// take removes up to n from slot i and returns what was removed.
func (v *View) take(i, n int) Stack {
	st := v.Slots[i]
	n = min(n, int(st.Count))
	if n <= 0 {
		return Stack{}
	}
	v.Slots[i] = WithCount(st, int(st.Count)-n)
	return WithCount(st, n)
}

// safeInsert is vanilla Slot.safeInsert: move up to n of st into slot i,
// returning what is left of st.
func (v *View) safeInsert(i int, st Stack, n int) Stack {
	if st.Empty() || !v.mayPlace(i, st) {
		return st
	}
	cur := v.Slots[i]
	n = min(n, int(st.Count), v.maxIn(i, st)-int(cur.Count))
	if n <= 0 {
		return st
	}
	switch {
	case cur.Empty():
		v.Slots[i] = WithCount(st, n)
	case same(cur, st):
		v.Slots[i] = WithCount(cur, int(cur.Count)+n)
	default:
		return st
	}
	return WithCount(st, int(st.Count)-n)
}

// quickReplaceable is vanilla canItemQuickReplace(slot, stack, true).
func (v *View) quickReplaceable(i int, st Stack) bool {
	cur := v.Slots[i]
	if cur.Empty() {
		return true
	}
	return same(cur, st) && int(cur.Count) <= MaxStackFor(st.ID)
}

// drop queues st to be thrown from the player.
func (v *View) drop(st Stack) {
	if !st.Empty() {
		v.Drops = append(v.Drops, st)
	}
}

// Click runs one Click Container. drag carries a drag-click across packets.
func (v *View) Click(slot, button, mode int, drag *Drag) {
	if mode == ClickQuickCraft {
		v.quickCraft(slot, button, drag)
		return
	}
	if drag.status != 0 {
		// Any other click cancels a drag in progress, and does nothing else.
		drag.Reset()
		return
	}
	switch mode {
	case ClickPickup, ClickQuickMove:
		if button != 0 && button != 1 {
			return
		}
		switch {
		case slot == SlotOutside:
			if !v.Cursor.Empty() {
				if button == 0 {
					v.drop(v.Cursor)
					v.Cursor = Stack{}
				} else {
					v.drop(WithCount(v.Cursor, 1))
					v.Cursor = WithCount(v.Cursor, int(v.Cursor.Count)-1)
				}
			}
		case !v.valid(slot):
		case mode == ClickQuickMove:
			// Repeat while part of the stack moved and the rest is still the
			// same item (vanilla's loop); quickMove reports no progress as false.
			for v.quickMove(slot) && !v.Slots[slot].Empty() {
			}
		default:
			v.pickup(slot, button)
		}
	case ClickSwap:
		if v.valid(slot) && (button >= 0 && button < 9 || button == OffhandSwapKey) {
			v.swap(slot, button)
		}
	case ClickClone:
		if v.Creative && v.Cursor.Empty() && v.valid(slot) && !v.Slots[slot].Empty() {
			st := v.Slots[slot]
			v.Cursor = WithCount(st, MaxStackFor(st.ID))
		}
	case ClickThrow:
		if v.Cursor.Empty() && v.valid(slot) && !v.Slots[slot].Empty() {
			n := 1
			if button != 0 {
				n = int(v.Slots[slot].Count)
			}
			v.drop(v.take(slot, n))
		}
	case ClickPickupAll:
		if v.valid(slot) {
			v.pickupAll(slot, button)
		}
	}
}

// pickup is a plain left (button 0) / right (1) click on a slot.
func (v *View) pickup(i, button int) {
	cur, carried := v.Slots[i], v.Cursor
	switch {
	case cur.Empty():
		if !carried.Empty() {
			n := int(carried.Count)
			if button == 1 {
				n = 1
			}
			v.Cursor = v.safeInsert(i, carried, n)
		}
	case carried.Empty():
		n := int(cur.Count)
		if button == 1 {
			n = (n + 1) / 2
		}
		v.Cursor = v.take(i, n)
	case v.mayPlace(i, carried):
		if same(cur, carried) {
			n := int(carried.Count)
			if button == 1 {
				n = 1
			}
			v.Cursor = v.safeInsert(i, carried, n)
		} else if int(carried.Count) <= v.maxIn(i, carried) {
			v.Slots[i], v.Cursor = carried, cur
		}
	case same(cur, carried):
		// A slot that takes nothing (crafting result) still gives.
		got := v.take(i, MaxStackFor(carried.ID)-int(carried.Count))
		v.Cursor = WithCount(carried, int(carried.Count)+int(got.Count))
	}
}

// swap is the number-key / F swap between slot i and a hotbar slot or the
// off hand.
func (v *View) swap(i, button int) {
	var hot *Stack
	switch {
	case button == OffhandSwapKey && v.Chest:
		hot = &v.Offhand
	case button == OffhandSwapKey:
		hot = &v.Slots[OffhandSlot]
	default:
		hot = &v.Slots[v.hotbarSlot(button)]
	}
	h, cur := *hot, v.Slots[i]
	switch {
	case h.Empty() && cur.Empty():
	case h.Empty():
		*hot = cur
		v.Slots[i] = Stack{}
	case cur.Empty():
		if !v.mayPlace(i, h) {
			return
		}
		if limit := v.maxIn(i, h); int(h.Count) > limit {
			v.Slots[i] = WithCount(h, limit)
			*hot = WithCount(h, int(h.Count)-limit)
		} else {
			*hot = Stack{}
			v.Slots[i] = h
		}
	case v.mayPlace(i, h):
		if limit := v.maxIn(i, h); int(h.Count) > limit {
			v.Slots[i] = WithCount(h, limit)
			*hot = WithCount(h, int(h.Count)-limit)
			v.addToPlayer(cur)
		} else {
			*hot, v.Slots[i] = cur, h
		}
	}
}

// addToPlayer is vanilla Inventory.add for the window's player slots: top
// up matching stacks, then the first free slot (hotbar first); the rest is
// dropped.
func (v *View) addToPlayer(st Stack) {
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
		if same(v.Slots[i], st) {
			st = v.safeInsert(i, st, int(st.Count))
		}
	}
	for _, i := range order {
		if v.Slots[i].Empty() {
			st = v.safeInsert(i, st, int(st.Count))
		}
	}
	v.drop(st)
}

// pickupAll is the double click: gather the cursor's item from every slot,
// non-full stacks on the first pass, full ones on the second.
func (v *View) pickupAll(i, button int) {
	carried := v.Cursor
	if carried.Empty() || !v.Slots[i].Empty() {
		return
	}
	first, step := 0, 1
	if button != 0 {
		first, step = len(v.Slots)-1, -1
	}
	limit := MaxStackFor(carried.ID)
	for pass := 0; pass < 2; pass++ {
		for k := first; k >= 0 && k < len(v.Slots) && int(carried.Count) < limit; k += step {
			st := v.Slots[k]
			if st.Empty() || !same(st, carried) || (!v.Chest && k == CraftResultSlot) {
				continue
			}
			if pass == 0 && int(st.Count) == MaxStackFor(st.ID) {
				continue
			}
			got := v.take(k, limit-int(carried.Count))
			carried = WithCount(carried, int(carried.Count)+int(got.Count))
		}
	}
	v.Cursor = carried
}

// quickCraft advances a drag-click: button = header (0 start, 1 add slot,
// 2 end) | type<<2.
func (v *View) quickCraft(slot, button int, d *Drag) {
	prev := d.status
	header, typ := button&3, (button>>2)&3
	d.status = header
	switch {
	case (prev != 1 || header != 2) && prev != header:
		d.Reset()
	case v.Cursor.Empty():
		d.Reset()
	case header == 0:
		if typ == 0 || typ == 1 || (typ == 2 && v.Creative) {
			d.status, d.typ, d.Slots = 1, typ, nil
		} else {
			d.Reset()
		}
	case header == 1:
		if !v.valid(slot) {
			return
		}
		for _, s := range d.Slots {
			if s == slot {
				return // a set: each slot once
			}
		}
		if v.quickReplaceable(slot, v.Cursor) && v.mayPlace(slot, v.Cursor) &&
			(d.typ == 2 || int(v.Cursor.Count) > len(d.Slots)) {
			d.Slots = append(d.Slots, slot)
		}
	case header == 2:
		slots, typ := d.Slots, d.typ
		d.Reset()
		if len(slots) == 1 {
			// One slot: an ordinary click with the drag's button.
			v.Click(slots[0], typ, ClickPickup, d)
			return
		}
		if len(slots) == 0 {
			return
		}
		carried := v.Cursor
		left := int(carried.Count)
		for _, s := range slots {
			if !v.quickReplaceable(s, carried) || !v.mayPlace(s, carried) ||
				(typ != 2 && int(carried.Count) < len(slots)) {
				continue
			}
			have := int(v.Slots[s].Count)
			limit := min(MaxStackFor(carried.ID), v.maxIn(s, carried))
			per := 1
			switch typ {
			case 0:
				per = int(carried.Count) / len(slots)
			case 2:
				per = MaxStackFor(carried.ID)
			}
			n := min(per+have, limit)
			left -= n - have
			v.Slots[s] = WithCount(carried, n)
		}
		v.Cursor = WithCount(carried, left)
	default:
		d.Reset()
	}
}

// quickMove is vanilla quickMoveStack for one shift-click on slot i: true
// when something moved.
func (v *View) quickMove(i int) bool {
	st := v.Slots[i]
	if st.Empty() {
		return false
	}
	before := st.Count
	if v.Chest {
		if i < ChestSlots {
			st = v.moveTo(st, chestPlayerStart, len(v.Slots), true)
		} else {
			st = v.moveTo(st, 0, ChestSlots, false)
		}
	} else {
		piece, worn := equipmentSlotOf(itemNameOf(st.ID))
		switch {
		case i == CraftResultSlot:
			return false // never holds anything
		case i < ArmorStart: // crafting grid
			st = v.moveTo(st, MainStart, OffhandSlot, false)
		case i < MainStart: // armor
			st = v.moveTo(st, MainStart, OffhandSlot, false)
		case worn && piece < 4 && v.Slots[ArmorStart+piece].Empty():
			st = v.moveTo(st, ArmorStart+piece, ArmorStart+piece+1, false)
		case worn && piece == 4 && v.Slots[OffhandSlot].Empty():
			st = v.moveTo(st, OffhandSlot, OffhandSlot+1, false)
		case i < HotbarStart:
			st = v.moveTo(st, HotbarStart, HotbarStart+9, false)
		case i < HotbarStart+9:
			st = v.moveTo(st, MainStart, HotbarStart, false)
		default: // off hand
			st = v.moveTo(st, MainStart, OffhandSlot, false)
		}
	}
	v.Slots[i] = st
	return st.Count != before
}

// moveTo is vanilla moveItemStackTo: merge st into matching stacks of
// [start, end), then into the first empty slot; returns what is left.
func (v *View) moveTo(st Stack, start, end int, reverse bool) Stack {
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
	if MaxStackFor(st.ID) > 1 {
		for i := first; !st.Empty() && i >= start && i < end; i = next(i) {
			cur := v.Slots[i]
			if !same(cur, st) {
				continue
			}
			limit := min(v.maxIn(i, st), MaxStackFor(st.ID))
			if total := int(cur.Count) + int(st.Count); total <= limit {
				v.Slots[i] = WithCount(cur, total)
				st = Stack{}
			} else if int(cur.Count) < limit {
				st = WithCount(st, total-limit)
				v.Slots[i] = WithCount(cur, limit)
			}
		}
	}
	if !st.Empty() {
		for i := first; i >= start && i < end; i = next(i) {
			if v.Slots[i].Empty() && v.mayPlace(i, st) {
				n := min(int(st.Count), v.maxIn(i, st))
				v.Slots[i] = WithCount(st, n)
				st = WithCount(st, int(st.Count)-n)
				break
			}
		}
	}
	return st
}

// Claim is the client's prediction from a Click Container: the slots it
// says changed and the item it says it carries afterwards.
type Claim struct {
	Slots  map[int]Stack
	Cursor Stack
}

// ReadClaim parses the changed-slots array + carried item.
func ReadClaim(packet *bytes.Buffer) (Claim, error) {
	n, err := protocol.ReadVarInt(packet)
	if err != nil {
		return Claim{}, fmt.Errorf("changed slot count: %w", err)
	}
	if n < 0 || n > maxClaimedSlots {
		return Claim{}, fmt.Errorf("%d changed slots", n)
	}
	claim := Claim{Slots: make(map[int]Stack, n)}
	for k := 0; k < n; k++ {
		raw, err := protocol.ReadUShortFromBuf(packet)
		if err != nil {
			return Claim{}, fmt.Errorf("changed slot index: %w", err)
		}
		st, ok := ReadSlot(packet)
		if !ok {
			return Claim{}, fmt.Errorf("changed slot %d item", int16(raw))
		}
		claim.Slots[int(int16(raw))] = st
	}
	cursor, ok := ReadSlot(packet)
	if !ok {
		return Claim{}, fmt.Errorf("carried item")
	}
	claim.Cursor = cursor
	return claim, nil
}

// Equal: same count and, when non-empty, the same item data.
func Equal(a, b Stack) bool {
	return a.Count == b.Count && (a.Empty() || a.SameKind(b))
}

// Matches reports whether the client predicted exactly the simulated result:
// every slot it reported holds what it says, every slot that changed was
// reported, and the cursor agrees.
func (c Claim) Matches(before []Stack, v *View) bool {
	// NoDrop is server-only: the client's copy never carries it.
	seen := func(claimed, server Stack) bool {
		claimed.NoDrop = server.NoDrop
		return Equal(claimed, server)
	}
	if !seen(c.Cursor, v.Cursor) {
		return false
	}
	for i, st := range c.Slots {
		if !v.valid(i) || !seen(st, v.Slots[i]) {
			return false
		}
	}
	for i := range v.Slots {
		if _, reported := c.Slots[i]; !reported && !Equal(before[i], v.Slots[i]) {
			return false
		}
	}
	return true
}

// StoresUndroppable reports whether a click moved an undroppable stack into
// the chest: a chest slot holds one now that didn't hold that stack before.
func StoresUndroppable(before, after []Stack) bool {
	for i := range after {
		if after[i].Undroppable() && !Equal(before[i], after[i]) {
			return true
		}
	}
	return false
}
