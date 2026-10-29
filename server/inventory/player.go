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
