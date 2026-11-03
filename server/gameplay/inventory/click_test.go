package inventory

import (
	"math/rand"
	"testing"

	"minecraft-server/world"
)

// click_test.go: the Click Container simulation (click.go) against vanilla
// 1.20.1 behaviour, plus the property that matters for anticheat — outside
// creative, no sequence of clicks creates items.

func itemID(t *testing.T, name string) int32 {
	t.Helper()
	id, ok := world.ItemByName(name)
	if !ok {
		t.Fatalf("unknown item %q", name)
	}
	return id
}

func stack(t *testing.T, name string, n int) Stack {
	return Stack{ID: itemID(t, name), Count: byte(n)}
}

// invView is an empty window-0 view.
func invView() *View { return &View{Slots: make([]Stack, PlayerSlots)} }

// chestView is an empty chest window view.
func chestView() *View {
	return &View{Slots: make([]Stack, ChestSlots+36), Chest: true}
}

func wantSlot(t *testing.T, v *View, i int, want Stack) {
	t.Helper()
	if !Equal(v.Slots[i], want) {
		t.Fatalf("slot %d = %+v, want %+v", i, v.Slots[i], want)
	}
}

func wantCursor(t *testing.T, v *View, want Stack) {
	t.Helper()
	if !Equal(v.Cursor, want) {
		t.Fatalf("cursor = %+v, want %+v", v.Cursor, want)
	}
}

func TestClickPickupPlaceSplitMerge(t *testing.T) {
	var d Drag
	v := invView()
	v.Slots[36] = stack(t, "minecraft:iron_ingot", 9)

	v.Click(36, 1, ClickPickup, &d) // right: half, rounded up
	wantCursor(t, v, stack(t, "minecraft:iron_ingot", 5))
	wantSlot(t, v, 36, stack(t, "minecraft:iron_ingot", 4))

	v.Click(37, 1, ClickPickup, &d) // right on empty: place one
	wantSlot(t, v, 37, stack(t, "minecraft:iron_ingot", 1))
	wantCursor(t, v, stack(t, "minecraft:iron_ingot", 4))

	v.Click(36, 0, ClickPickup, &d) // left on same item: merge all
	wantSlot(t, v, 36, stack(t, "minecraft:iron_ingot", 8))
	wantCursor(t, v, Stack{})
}

func TestClickMergeCapsAtStackSize(t *testing.T) {
	var d Drag
	v := invView()
	v.Slots[36] = stack(t, "minecraft:ender_pearl", 10)
	v.Cursor = stack(t, "minecraft:ender_pearl", 10)
	v.Click(36, 0, ClickPickup, &d)
	wantSlot(t, v, 36, stack(t, "minecraft:ender_pearl", 16))
	wantCursor(t, v, stack(t, "minecraft:ender_pearl", 4))
}

func TestClickSwapDifferentItems(t *testing.T) {
	var d Drag
	v := invView()
	v.Slots[36] = stack(t, "minecraft:stone", 5)
	v.Cursor = stack(t, "minecraft:dirt", 3)
	v.Click(36, 0, ClickPickup, &d)
	wantSlot(t, v, 36, stack(t, "minecraft:dirt", 3))
	wantCursor(t, v, stack(t, "minecraft:stone", 5))
}

func TestClickArmorSlots(t *testing.T) {
	var d Drag
	v := invView()
	v.Cursor = stack(t, "minecraft:stone", 1)
	v.Click(ArmorStart, 0, ClickPickup, &d) // stone isn't a helmet
	wantSlot(t, v, ArmorStart, Stack{})

	v.Cursor = stack(t, "minecraft:iron_boots", 1)
	v.Click(ArmorStart, 0, ClickPickup, &d) // boots aren't a helmet either
	wantSlot(t, v, ArmorStart, Stack{})
	v.Click(ArmorStart+3, 0, ClickPickup, &d)
	wantSlot(t, v, ArmorStart+3, stack(t, "minecraft:iron_boots", 1))

	// Shift-click a helmet from the inventory: it goes on the head.
	v.Slots[20] = stack(t, "minecraft:diamond_helmet", 1)
	v.Click(20, 0, ClickQuickMove, &d)
	wantSlot(t, v, ArmorStart, stack(t, "minecraft:diamond_helmet", 1))
	wantSlot(t, v, 20, Stack{})
}

func TestClickCraftResultNeverYields(t *testing.T) {
	var d Drag
	v := invView()
	v.Slots[CraftGridStart] = stack(t, "minecraft:oak_log", 1)
	v.Click(CraftResultSlot, 0, ClickPickup, &d)
	v.Click(CraftResultSlot, 0, ClickQuickMove, &d)
	wantCursor(t, v, Stack{})
	if total := totalItems(v); total != 1 {
		t.Fatalf("crafting produced items: total %d", total)
	}
}

func TestClickQuickMoveInventory(t *testing.T) {
	var d Drag
	v := invView()
	v.Slots[36] = stack(t, "minecraft:stone", 64) // hotbar → main
	v.Slots[9] = stack(t, "minecraft:stone", 60)
	v.Click(36, 0, ClickQuickMove, &d)
	wantSlot(t, v, 9, stack(t, "minecraft:stone", 64))
	wantSlot(t, v, 10, stack(t, "minecraft:stone", 60))
	wantSlot(t, v, 36, Stack{})

	v.Click(10, 0, ClickQuickMove, &d) // main → hotbar
	wantSlot(t, v, 36, stack(t, "minecraft:stone", 60))
}

func TestClickQuickMoveChest(t *testing.T) {
	var d Drag
	v := chestView()
	v.Slots[0] = stack(t, "minecraft:diamond", 5)
	v.Click(0, 0, ClickQuickMove, &d) // chest → player, last hotbar slot first
	wantSlot(t, v, len(v.Slots)-1, stack(t, "minecraft:diamond", 5))

	v.Click(len(v.Slots)-1, 0, ClickQuickMove, &d) // player → first chest slot
	wantSlot(t, v, 0, stack(t, "minecraft:diamond", 5))
}

func TestClickNumberKeyAndOffhandSwap(t *testing.T) {
	var d Drag
	v := invView()
	v.Slots[20] = stack(t, "minecraft:stone", 3)
	v.Slots[38] = stack(t, "minecraft:dirt", 2)
	v.Click(20, 2, ClickSwap, &d) // key 3 → hotbar 2 = slot 38
	wantSlot(t, v, 20, stack(t, "minecraft:dirt", 2))
	wantSlot(t, v, 38, stack(t, "minecraft:stone", 3))

	v.Click(20, OffhandSwapKey, ClickSwap, &d)
	wantSlot(t, v, OffhandSlot, stack(t, "minecraft:dirt", 2))
	wantSlot(t, v, 20, Stack{})

	// Chest windows reach the off hand outside the window.
	cv := chestView()
	cv.Slots[3] = stack(t, "minecraft:emerald", 4)
	cv.Click(3, OffhandSwapKey, ClickSwap, &d)
	if !Equal(cv.Offhand, stack(t, "minecraft:emerald", 4)) || !cv.Slots[3].Empty() {
		t.Fatalf("chest F-swap: offhand %+v slot %+v", cv.Offhand, cv.Slots[3])
	}
}

func TestClickThrowAndOutside(t *testing.T) {
	var d Drag
	v := invView()
	v.Slots[36] = stack(t, "minecraft:iron_ingot", 5)
	v.Click(36, 0, ClickThrow, &d) // Q: one
	v.Click(36, 1, ClickThrow, &d) // ctrl+Q: the rest
	if len(v.Drops) != 2 || v.Drops[0].Count != 1 || v.Drops[1].Count != 4 || !v.Slots[36].Empty() {
		t.Fatalf("throw: drops %+v slot %+v", v.Drops, v.Slots[36])
	}

	v.Drops = nil
	v.Cursor = stack(t, "minecraft:iron_ingot", 3)
	v.Click(SlotOutside, 1, ClickPickup, &d) // right outside: one
	v.Click(SlotOutside, 0, ClickPickup, &d) // left outside: all
	if len(v.Drops) != 2 || v.Drops[0].Count != 1 || v.Drops[1].Count != 2 || !v.Cursor.Empty() {
		t.Fatalf("outside: drops %+v cursor %+v", v.Drops, v.Cursor)
	}
}

func TestClickDragSplitEvenly(t *testing.T) {
	var d Drag
	v := invView()
	v.Cursor = stack(t, "minecraft:stone", 10)
	v.Click(-999, 0, ClickQuickCraft, &d) // start, left drag
	for _, s := range []int{36, 37, 38} {
		v.Click(s, 1, ClickQuickCraft, &d)
	}
	v.Click(-999, 2, ClickQuickCraft, &d) // end
	for _, s := range []int{36, 37, 38} {
		wantSlot(t, v, s, stack(t, "minecraft:stone", 3))
	}
	wantCursor(t, v, stack(t, "minecraft:stone", 1))
}

func TestClickDragOneEach(t *testing.T) {
	var d Drag
	v := invView()
	v.Cursor = stack(t, "minecraft:stone", 10)
	v.Click(-999, 4, ClickQuickCraft, &d) // start, right drag (type 1)
	v.Click(36, 5, ClickQuickCraft, &d)
	v.Click(37, 5, ClickQuickCraft, &d)
	v.Click(-999, 6, ClickQuickCraft, &d)
	wantSlot(t, v, 36, stack(t, "minecraft:stone", 1))
	wantSlot(t, v, 37, stack(t, "minecraft:stone", 1))
	wantCursor(t, v, stack(t, "minecraft:stone", 8))
}

func TestClickMiddleDragNeedsCreative(t *testing.T) {
	var d Drag
	v := invView()
	v.Cursor = stack(t, "minecraft:stone", 1)
	v.Click(-999, 8, ClickQuickCraft, &d) // middle drag (type 2)
	v.Click(36, 9, ClickQuickCraft, &d)
	v.Click(37, 9, ClickQuickCraft, &d)
	v.Click(-999, 10, ClickQuickCraft, &d)
	if total := totalItems(v); total != 1 {
		t.Fatalf("survival middle-drag created items: total %d", total)
	}

	v.Creative = true
	v.Click(-999, 8, ClickQuickCraft, &d)
	v.Click(36, 9, ClickQuickCraft, &d)
	v.Click(37, 9, ClickQuickCraft, &d)
	v.Click(-999, 10, ClickQuickCraft, &d)
	wantSlot(t, v, 36, stack(t, "minecraft:stone", 64))
}

func TestClickDragCancelledByOtherClick(t *testing.T) {
	var d Drag
	v := invView()
	v.Cursor = stack(t, "minecraft:stone", 10)
	v.Click(-999, 0, ClickQuickCraft, &d)
	v.Click(36, 1, ClickQuickCraft, &d)
	v.Click(37, 0, ClickPickup, &d) // cancels the drag, does nothing else
	if d.status != 0 || !v.Slots[37].Empty() {
		t.Fatalf("drag not cancelled: status %d slot37 %+v", d.status, v.Slots[37])
	}
}

func TestClickDoubleClickGathers(t *testing.T) {
	var d Drag
	v := invView()
	v.Cursor = stack(t, "minecraft:stone", 10)
	v.Slots[9] = stack(t, "minecraft:stone", 64) // full: second pass only
	v.Slots[10] = stack(t, "minecraft:stone", 20)
	v.Slots[11] = stack(t, "minecraft:dirt", 5)
	v.Click(36, 0, ClickPickupAll, &d)
	wantCursor(t, v, stack(t, "minecraft:stone", 64))
	wantSlot(t, v, 10, Stack{})                        // non-full taken first
	wantSlot(t, v, 9, stack(t, "minecraft:stone", 30)) // then the full one
	wantSlot(t, v, 11, stack(t, "minecraft:dirt", 5))  // other items untouched
}

func TestClickCloneOnlyInCreative(t *testing.T) {
	var d Drag
	v := invView()
	v.Slots[36] = stack(t, "minecraft:diamond", 1)
	v.Click(36, 2, ClickClone, &d)
	wantCursor(t, v, Stack{})
	v.Creative = true
	v.Click(36, 2, ClickClone, &d)
	wantCursor(t, v, stack(t, "minecraft:diamond", 64))
}

func TestClickKeepsServerEnchantments(t *testing.T) {
	var d Drag
	v := invView()
	sword := Stack{ID: itemID(t, "minecraft:iron_sword"), Count: 1, Enchantments: map[string]int{"minecraft:sharpness": 1}}
	v.Slots[36] = sword
	v.Click(36, 0, ClickPickup, &d)
	v.Click(37, 0, ClickPickup, &d)
	if got := v.Slots[37]; !got.SameKind(sword) || got.EnchantLevel("minecraft:sharpness") != 1 {
		t.Fatalf("moved sword = %+v, want the server's Sharpness I", got)
	}
}

func TestClickClaimMatching(t *testing.T) {
	var d Drag
	v := invView()
	v.Slots[36] = stack(t, "minecraft:stone", 4)
	before := append([]Stack(nil), v.Slots...)
	v.Click(36, 0, ClickPickup, &d)

	honest := Claim{Slots: map[int]Stack{36: {}}, Cursor: stack(t, "minecraft:stone", 4)}
	if !honest.Matches(before, v) {
		t.Fatal("the vanilla prediction should match")
	}
	forged := Claim{Slots: map[int]Stack{36: {}}, Cursor: stack(t, "minecraft:stone", 64)}
	if forged.Matches(before, v) {
		t.Fatal("a forged cursor should not match")
	}
	silent := Claim{Cursor: stack(t, "minecraft:stone", 4)}
	if silent.Matches(before, v) {
		t.Fatal("an unreported changed slot should not match")
	}
	enchanted := Claim{Slots: map[int]Stack{36: {}}, Cursor: Stack{
		ID: itemID(t, "minecraft:stone"), Count: 4, Enchantments: map[string]int{"minecraft:sharpness": 255}}}
	if enchanted.Matches(before, v) {
		t.Fatal("client-side enchantments should not match")
	}
}

// totalItems counts every unit in the view: slots, cursor, off hand, drops.
func totalItems(v *View) int {
	n := int(v.Cursor.Count) + int(v.Offhand.Count)
	for _, st := range v.Slots {
		n += int(st.Count)
	}
	for _, st := range v.Drops {
		n += int(st.Count)
	}
	return n
}

// TestClickNeverCreatesItems fuzzes random clicks (any slot, button, mode)
// over both window kinds: outside creative the number of items — in slots,
// on the cursor, in the off hand or thrown — never changes.
func TestClickNeverCreatesItems(t *testing.T) {
	items := []Stack{
		stack(t, "minecraft:stone", 1), stack(t, "minecraft:ender_pearl", 1),
		stack(t, "minecraft:iron_helmet", 1), stack(t, "minecraft:diamond", 1),
		stack(t, "minecraft:shield", 1),
	}
	rng := rand.New(rand.NewSource(1))
	for round := 0; round < 300; round++ {
		var v *View
		if round%2 == 0 {
			v = invView()
		} else {
			v = chestView()
		}
		for i := range v.Slots {
			if rng.Intn(3) == 0 {
				st := items[rng.Intn(len(items))]
				v.Slots[i] = WithCount(st, 1+rng.Intn(MaxStackFor(st.ID)))
			}
		}
		if !v.Chest {
			v.Slots[CraftResultSlot] = Stack{}
		}
		want := totalItems(v)
		var d Drag
		for step := 0; step < 200; step++ {
			slot := rng.Intn(len(v.Slots)+2) - 1
			if slot == -1 && rng.Intn(2) == 0 {
				slot = SlotOutside
			}
			button := rng.Intn(11)
			if rng.Intn(10) == 0 {
				button = OffhandSwapKey
			}
			v.Click(slot, button, rng.Intn(7), &d)
			if got := totalItems(v); got != want {
				t.Fatalf("round %d step %d: %d items, want %d (slot %d button %d)", round, step, got, want, slot, button)
			}
			for i, st := range v.Slots {
				if int(st.Count) > MaxStackFor(st.ID) {
					t.Fatalf("slot %d over-stacked: %+v", i, st)
				}
			}
		}
	}
}

// TestClickDragAcrossWindowsDropped: a drag started in a chest window and
// ended in window 0 used to index window 0 with the chest's slot numbers
// (60 of 46) and panic; now the window change drops the drag.
func TestClickDragAcrossWindowsDropped(t *testing.T) {
	var d Drag
	chest := chestView()
	chest.Cursor = stack(t, "minecraft:stone", 10)
	chest.Click(-999, 0, ClickQuickCraft, &d)
	chest.Click(60, 1, ClickQuickCraft, &d)
	chest.Click(61, 1, ClickQuickCraft, &d)

	inv := invView()
	inv.Cursor = chest.Cursor
	inv.Click(-999, 2, ClickQuickCraft, &d) // must not panic
	wantCursor(t, inv, stack(t, "minecraft:stone", 10))
	if len(d.Slots) != 0 {
		t.Fatalf("drag kept across windows: %v", d.Slots)
	}
}
