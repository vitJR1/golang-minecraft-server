package inventory

import (
	"sync"

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

// Player holds one player's window-0 slots and the stack on the cursor. The
// zero value is an all-empty inventory.
//
// It is safe for concurrent use: the connection's readLoop clicks while the
// instance's tick goroutine gives (pickups), takes (shops) and wears tools
// and armour. Every method is atomic; Update runs a whole read-modify-write
// (a simulated click) under the lock.
type Player struct {
	mu     sync.Mutex
	slots  [PlayerSlots]Stack
	cursor Stack
}

// Set stores st at a window-0 slot (bounds-checked).
func (p *Player) Set(slot int16, st Stack) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.set(slot, st)
}

// Get returns the stack at a window-0 slot (empty for out-of-range).
func (p *Player) Get(slot int16) Stack {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.get(slot)
}

func (p *Player) set(slot int16, st Stack) {
	if slot >= 0 && int(slot) < PlayerSlots {
		p.slots[slot] = st
	}
}

func (p *Player) get(slot int16) Stack {
	if slot >= 0 && int(slot) < PlayerSlots {
		return p.slots[slot]
	}
	return Stack{}
}

// Held returns the stack in the selected hotbar slot (heldSlot 0..8).
func (p *Player) Held(heldSlot int32) Stack {
	return p.Get(int16(HotbarStart) + int16(heldSlot))
}

// Edit replaces the stack at slot with fn's result, atomically (tool and
// armour wear, swapping a bucket).
func (p *Player) Edit(slot int16, fn func(Stack) Stack) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.set(slot, fn(p.get(slot)))
}

// Swap exchanges two slots.
func (p *Player) Swap(a, b int16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sa, sb := p.get(a), p.get(b)
	p.set(a, sb)
	p.set(b, sa)
}

// Cursor is the stack the player carries on the cursor.
func (p *Player) Cursor() Stack {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cursor
}

// SetCursor replaces the carried stack.
func (p *Player) SetCursor(st Stack) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cursor = st
}

// Snapshot copies every slot and the cursor.
func (p *Player) Snapshot() (slots [PlayerSlots]Stack, cursor Stack) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.slots, p.cursor
}

// Update runs fn on the slots and the cursor under the lock: a whole click
// (copy out, simulate, copy back) can't interleave with a pickup. fn must
// not call other methods of p.
func (p *Player) Update(fn func(slots *[PlayerSlots]Stack, cursor *Stack)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(&p.slots, &p.cursor)
}

// Clear empties every slot and the cursor.
func (p *Player) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.slots = [PlayerSlots]Stack{}
	p.cursor = Stack{}
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
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Stack(nil), p.slots[MainStart:HotbarStart+9]...)
}

// Give adds st (with all its display / enchantment data): first topping up
// stacks of the same kind, then filling empty slots, both in GiveOrder
// (hotbar first), up to MaxStackFor. It returns the units that did not fit
// and the slots it changed.
func (p *Player) Give(st Stack) (left int, changed []int16) {
	if st.Empty() {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	remaining := int(st.Count)
	limit := MaxStackFor(st.ID)
	for pass := 0; pass < 2 && remaining > 0; pass++ {
		for _, slot := range GiveOrder {
			if remaining == 0 {
				break
			}
			cur := p.get(slot)
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
			p.set(slot, cur)
			changed = append(changed, slot)
		}
	}
	return remaining, changed
}

// Count sums the units of itemID across the main inventory + hotbar.
func (p *Player) Count(itemID int32) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count(itemID)
}

func (p *Player) count(itemID int32) int {
	n := 0
	for _, slot := range GiveOrder {
		if st := p.get(slot); !st.Empty() && st.ID == itemID {
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
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.count(itemID) < count {
		return false, nil
	}
	remaining := count
	for idx := len(GiveOrder) - 1; idx >= 0 && remaining > 0; idx-- {
		slot := GiveOrder[idx]
		st := p.get(slot)
		if st.Empty() || st.ID != itemID {
			continue
		}
		take := min(int(st.Count), remaining)
		p.set(slot, WithCount(st, int(st.Count)-take))
		remaining -= take
		changed = append(changed, slot)
	}
	return true, changed
}

// TakeOne removes one unit from slot; false when the slot is empty.
func (p *Player) TakeOne(slot int16) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.get(slot)
	if st.Empty() {
		return false
	}
	p.set(slot, WithCount(st, int(st.Count)-1))
	return true
}
