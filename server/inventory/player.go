package inventory

import (
	"minecraft-server/world"
)

// MaxStack is the vanilla stack ceiling for plain items; MaxStackFor
// narrows it per item (see world.MaxStackSizeOf).
const MaxStack = world.MaxStackSize

// MaxStackFor returns the vanilla max stack size of an item id.
func MaxStackFor(itemID int32) int {
	name, ok := world.ItemName(itemID)
	if !ok {
		return MaxStack
	}
	return world.MaxStackSizeOf(name)
}

// Player is the per-connection player inventory model. It mirrors the
// window-0 layout the client uses so the server knows what the player is
// actually holding — used to place only a held block (no more always-stone)
// and to pick the right throwable. It is authoritative: container clicks are
// simulated on it (click.go), creative slot edits are taken from creative
// players only; crafting isn't modelled.
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
	PlayerSlots = 46
	HotbarStart = 36 // window-0 slot of hotbar position 0
	MainStart   = 9  // window-0 slot where main inventory + hotbar begin
)

// Player holds one player's window-0 slots. The zero value is an
// all-empty inventory.
type Player struct {
	Slots [PlayerSlots]Stack
}

// Set stores st at a window-0 slot (bounds-checked).
func (inv *Player) Set(slot int16, st Stack) {
	if slot >= 0 && int(slot) < PlayerSlots {
		inv.Slots[slot] = st
	}
}

// Get returns the stack at a window-0 slot (empty for out-of-range).
func (inv *Player) Get(slot int16) Stack {
	if slot >= 0 && int(slot) < PlayerSlots {
		return inv.Slots[slot]
	}
	return Stack{}
}

// Held returns the stack in the selected hotbar slot (heldSlot 0..8).
func (inv *Player) Held(heldSlot int32) Stack {
	return inv.Get(int16(HotbarStart) + int16(heldSlot))
}

// GiveOrder is the slot visiting order for giveItem: the 9 hotbar slots
// first, then the 27 main-inventory slots.
var GiveOrder = func() []int16 {
	order := make([]int16, 0, 36)
	for slot := int16(HotbarStart); slot < HotbarStart+9; slot++ {
		order = append(order, slot)
	}
	for slot := int16(MainStart); slot < HotbarStart; slot++ {
		order = append(order, slot)
	}
	return order
}()

// Mirror is the 36 player-inventory slots a container window shows below its
// own (main 9..35, then the hotbar 36..44).
func (p *Player) Mirror() []Stack {
	return append([]Stack(nil), p.Slots[MainStart:HotbarStart+9]...)
}

// Give adds st (with all its display / enchantment data): first topping up
// stacks of the same kind, then filling empty slots, both in GiveOrder
// (hotbar first), up to MaxStackFor. It returns the units that did not fit
// and the slots it changed.
func (p *Player) Give(st Stack) (left int, changed []int16) {
	if st.Empty() {
		return 0, nil
	}
	remaining := int(st.Count)
	limit := MaxStackFor(st.ID)
	for pass := 0; pass < 2 && remaining > 0; pass++ {
		for _, slot := range GiveOrder {
			if remaining == 0 {
				break
			}
			cur := p.Get(slot)
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
			p.Set(slot, cur)
			changed = append(changed, slot)
		}
	}
	return remaining, changed
}

// Count sums the units of itemID across the main inventory + hotbar.
func (p *Player) Count(itemID int32) int {
	n := 0
	for _, slot := range GiveOrder {
		if st := p.Get(slot); !st.Empty() && st.ID == itemID {
			n += int(st.Count)
		}
	}
	return n
}

// Take removes count units of itemID, main inventory first so the hotbar
// keeps its stacks longest. It removes nothing and returns false when there
// are fewer than count; changed are the slots it emptied or shrank.
func (p *Player) Take(itemID int32, count int) (ok bool, changed []int16) {
	if count <= 0 {
		return true, nil
	}
	if p.Count(itemID) < count {
		return false, nil
	}
	remaining := count
	for idx := len(GiveOrder) - 1; idx >= 0 && remaining > 0; idx-- {
		slot := GiveOrder[idx]
		st := p.Get(slot)
		if st.Empty() || st.ID != itemID {
			continue
		}
		take := min(int(st.Count), remaining)
		p.Set(slot, WithCount(st, int(st.Count)-take))
		remaining -= take
		changed = append(changed, slot)
	}
	return true, changed
}

// TakeOne removes one unit from slot; false when the slot is empty.
func (p *Player) TakeOne(slot int16) bool {
	st := p.Get(slot)
	if st.Empty() {
		return false
	}
	p.Set(slot, WithCount(st, int(st.Count)-1))
	return true
}
