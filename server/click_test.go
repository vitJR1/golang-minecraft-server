package server

import (
	"math/rand"
	"testing"

	"minecraft-server/player"
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

func stack(t *testing.T, name string, n int) itemStack {
	return itemStack{ID: itemID(t, name), Count: byte(n)}
}

// invView is an empty window-0 view.
func invView() *clickView { return &clickView{slots: make([]itemStack, playerInvSize)} }

// chestView is an empty chest window view.
func chestView() *clickView {
	return &clickView{slots: make([]itemStack, chestSlotCount+36), chest: true}
}

func wantSlot(t *testing.T, v *clickView, i int, want itemStack) {
	t.Helper()
	if !stackEqual(v.slots[i], want) {
		t.Fatalf("slot %d = %+v, want %+v", i, v.slots[i], want)
	}
}

func wantCursor(t *testing.T, v *clickView, want itemStack) {
	t.Helper()
	if !stackEqual(v.cursor, want) {
		t.Fatalf("cursor = %+v, want %+v", v.cursor, want)
	}
}

func TestClickPickupPlaceSplitMerge(t *testing.T) {
	var d dragState
	v := invView()
	v.slots[36] = stack(t, "minecraft:iron_ingot", 9)

	v.click(36, 1, clickPickup, &d) // right: half, rounded up
	wantCursor(t, v, stack(t, "minecraft:iron_ingot", 5))
	wantSlot(t, v, 36, stack(t, "minecraft:iron_ingot", 4))

	v.click(37, 1, clickPickup, &d) // right on empty: place one
	wantSlot(t, v, 37, stack(t, "minecraft:iron_ingot", 1))
	wantCursor(t, v, stack(t, "minecraft:iron_ingot", 4))

	v.click(36, 0, clickPickup, &d) // left on same item: merge all
	wantSlot(t, v, 36, stack(t, "minecraft:iron_ingot", 8))
	wantCursor(t, v, itemStack{})
}

func TestClickMergeCapsAtStackSize(t *testing.T) {
	var d dragState
	v := invView()
	v.slots[36] = stack(t, "minecraft:ender_pearl", 10)
	v.cursor = stack(t, "minecraft:ender_pearl", 10)
	v.click(36, 0, clickPickup, &d)
	wantSlot(t, v, 36, stack(t, "minecraft:ender_pearl", 16))
	wantCursor(t, v, stack(t, "minecraft:ender_pearl", 4))
}

func TestClickSwapDifferentItems(t *testing.T) {
	var d dragState
	v := invView()
	v.slots[36] = stack(t, "minecraft:stone", 5)
	v.cursor = stack(t, "minecraft:dirt", 3)
	v.click(36, 0, clickPickup, &d)
	wantSlot(t, v, 36, stack(t, "minecraft:dirt", 3))
	wantCursor(t, v, stack(t, "minecraft:stone", 5))
}

func TestClickArmorSlots(t *testing.T) {
	var d dragState
	v := invView()
	v.cursor = stack(t, "minecraft:stone", 1)
	v.click(armorStart, 0, clickPickup, &d) // stone isn't a helmet
	wantSlot(t, v, armorStart, itemStack{})

	v.cursor = stack(t, "minecraft:iron_boots", 1)
	v.click(armorStart, 0, clickPickup, &d) // boots aren't a helmet either
	wantSlot(t, v, armorStart, itemStack{})
	v.click(armorStart+3, 0, clickPickup, &d)
	wantSlot(t, v, armorStart+3, stack(t, "minecraft:iron_boots", 1))

	// Shift-click a helmet from the inventory: it goes on the head.
	v.slots[20] = stack(t, "minecraft:diamond_helmet", 1)
	v.click(20, 0, clickQuickMove, &d)
	wantSlot(t, v, armorStart, stack(t, "minecraft:diamond_helmet", 1))
	wantSlot(t, v, 20, itemStack{})
}

func TestClickCraftResultNeverYields(t *testing.T) {
	var d dragState
	v := invView()
	v.slots[craftGridStart] = stack(t, "minecraft:oak_log", 1)
	v.click(craftResultSlot, 0, clickPickup, &d)
	v.click(craftResultSlot, 0, clickQuickMove, &d)
	wantCursor(t, v, itemStack{})
	if total := totalItems(v); total != 1 {
		t.Fatalf("crafting produced items: total %d", total)
	}
}

func TestClickQuickMoveInventory(t *testing.T) {
	var d dragState
	v := invView()
	v.slots[36] = stack(t, "minecraft:stone", 64) // hotbar → main
	v.slots[9] = stack(t, "minecraft:stone", 60)
	v.click(36, 0, clickQuickMove, &d)
	wantSlot(t, v, 9, stack(t, "minecraft:stone", 64))
	wantSlot(t, v, 10, stack(t, "minecraft:stone", 60))
	wantSlot(t, v, 36, itemStack{})

	v.click(10, 0, clickQuickMove, &d) // main → hotbar
	wantSlot(t, v, 36, stack(t, "minecraft:stone", 60))
}

func TestClickQuickMoveChest(t *testing.T) {
	var d dragState
	v := chestView()
	v.slots[0] = stack(t, "minecraft:diamond", 5)
	v.click(0, 0, clickQuickMove, &d) // chest → player, last hotbar slot first
	wantSlot(t, v, len(v.slots)-1, stack(t, "minecraft:diamond", 5))

	v.click(len(v.slots)-1, 0, clickQuickMove, &d) // player → first chest slot
	wantSlot(t, v, 0, stack(t, "minecraft:diamond", 5))
}

func TestClickNumberKeyAndOffhandSwap(t *testing.T) {
	var d dragState
	v := invView()
	v.slots[20] = stack(t, "minecraft:stone", 3)
	v.slots[38] = stack(t, "minecraft:dirt", 2)
	v.click(20, 2, clickSwap, &d) // key 3 → hotbar 2 = slot 38
	wantSlot(t, v, 20, stack(t, "minecraft:dirt", 2))
	wantSlot(t, v, 38, stack(t, "minecraft:stone", 3))

	v.click(20, offhandSwapKey, clickSwap, &d)
	wantSlot(t, v, offhandSlot, stack(t, "minecraft:dirt", 2))
	wantSlot(t, v, 20, itemStack{})

	// Chest windows reach the off hand outside the window.
	cv := chestView()
	cv.slots[3] = stack(t, "minecraft:emerald", 4)
	cv.click(3, offhandSwapKey, clickSwap, &d)
	if !stackEqual(cv.offhand, stack(t, "minecraft:emerald", 4)) || !cv.slots[3].empty() {
		t.Fatalf("chest F-swap: offhand %+v slot %+v", cv.offhand, cv.slots[3])
	}
}

func TestClickThrowAndOutside(t *testing.T) {
	var d dragState
	v := invView()
	v.slots[36] = stack(t, "minecraft:iron_ingot", 5)
	v.click(36, 0, clickThrow, &d) // Q: one
	v.click(36, 1, clickThrow, &d) // ctrl+Q: the rest
	if len(v.drops) != 2 || v.drops[0].Count != 1 || v.drops[1].Count != 4 || !v.slots[36].empty() {
		t.Fatalf("throw: drops %+v slot %+v", v.drops, v.slots[36])
	}

	v.drops = nil
	v.cursor = stack(t, "minecraft:iron_ingot", 3)
	v.click(slotOutside, 1, clickPickup, &d) // right outside: one
	v.click(slotOutside, 0, clickPickup, &d) // left outside: all
	if len(v.drops) != 2 || v.drops[0].Count != 1 || v.drops[1].Count != 2 || !v.cursor.empty() {
		t.Fatalf("outside: drops %+v cursor %+v", v.drops, v.cursor)
	}
}

func TestClickDragSplitEvenly(t *testing.T) {
	var d dragState
	v := invView()
	v.cursor = stack(t, "minecraft:stone", 10)
	v.click(-999, 0, clickQuickCraft, &d) // start, left drag
	for _, s := range []int{36, 37, 38} {
		v.click(s, 1, clickQuickCraft, &d)
	}
	v.click(-999, 2, clickQuickCraft, &d) // end
	for _, s := range []int{36, 37, 38} {
		wantSlot(t, v, s, stack(t, "minecraft:stone", 3))
	}
	wantCursor(t, v, stack(t, "minecraft:stone", 1))
}

func TestClickDragOneEach(t *testing.T) {
	var d dragState
	v := invView()
	v.cursor = stack(t, "minecraft:stone", 10)
	v.click(-999, 4, clickQuickCraft, &d) // start, right drag (type 1)
	v.click(36, 5, clickQuickCraft, &d)
	v.click(37, 5, clickQuickCraft, &d)
	v.click(-999, 6, clickQuickCraft, &d)
	wantSlot(t, v, 36, stack(t, "minecraft:stone", 1))
	wantSlot(t, v, 37, stack(t, "minecraft:stone", 1))
	wantCursor(t, v, stack(t, "minecraft:stone", 8))
}

func TestClickMiddleDragNeedsCreative(t *testing.T) {
	var d dragState
	v := invView()
	v.cursor = stack(t, "minecraft:stone", 1)
	v.click(-999, 8, clickQuickCraft, &d) // middle drag (type 2)
	v.click(36, 9, clickQuickCraft, &d)
	v.click(37, 9, clickQuickCraft, &d)
	v.click(-999, 10, clickQuickCraft, &d)
	if total := totalItems(v); total != 1 {
		t.Fatalf("survival middle-drag created items: total %d", total)
	}

	v.creative = true
	v.click(-999, 8, clickQuickCraft, &d)
	v.click(36, 9, clickQuickCraft, &d)
	v.click(37, 9, clickQuickCraft, &d)
	v.click(-999, 10, clickQuickCraft, &d)
	wantSlot(t, v, 36, stack(t, "minecraft:stone", 64))
}

func TestClickDragCancelledByOtherClick(t *testing.T) {
	var d dragState
	v := invView()
	v.cursor = stack(t, "minecraft:stone", 10)
	v.click(-999, 0, clickQuickCraft, &d)
	v.click(36, 1, clickQuickCraft, &d)
	v.click(37, 0, clickPickup, &d) // cancels the drag, does nothing else
	if d.status != 0 || !v.slots[37].empty() {
		t.Fatalf("drag not cancelled: status %d slot37 %+v", d.status, v.slots[37])
	}
}

func TestClickDoubleClickGathers(t *testing.T) {
	var d dragState
	v := invView()
	v.cursor = stack(t, "minecraft:stone", 10)
	v.slots[9] = stack(t, "minecraft:stone", 64) // full: second pass only
	v.slots[10] = stack(t, "minecraft:stone", 20)
	v.slots[11] = stack(t, "minecraft:dirt", 5)
	v.click(36, 0, clickPickupAll, &d)
	wantCursor(t, v, stack(t, "minecraft:stone", 64))
	wantSlot(t, v, 10, itemStack{})                    // non-full taken first
	wantSlot(t, v, 9, stack(t, "minecraft:stone", 30)) // then the full one
	wantSlot(t, v, 11, stack(t, "minecraft:dirt", 5))  // other items untouched
}

func TestClickCloneOnlyInCreative(t *testing.T) {
	var d dragState
	v := invView()
	v.slots[36] = stack(t, "minecraft:diamond", 1)
	v.click(36, 2, clickClone, &d)
	wantCursor(t, v, itemStack{})
	v.creative = true
	v.click(36, 2, clickClone, &d)
	wantCursor(t, v, stack(t, "minecraft:diamond", 64))
}

func TestClickKeepsServerEnchantments(t *testing.T) {
	var d dragState
	v := invView()
	sword := itemStack{ID: itemID(t, "minecraft:iron_sword"), Count: 1, Enchantments: map[string]int{"minecraft:sharpness": 1}}
	v.slots[36] = sword
	v.click(36, 0, clickPickup, &d)
	v.click(37, 0, clickPickup, &d)
	if got := v.slots[37]; !got.sameKind(sword) || got.enchantLevel("minecraft:sharpness") != 1 {
		t.Fatalf("moved sword = %+v, want the server's Sharpness I", got)
	}
}

func TestClickClaimMatching(t *testing.T) {
	var d dragState
	v := invView()
	v.slots[36] = stack(t, "minecraft:stone", 4)
	before := append([]itemStack(nil), v.slots...)
	v.click(36, 0, clickPickup, &d)

	honest := clickClaim{slots: map[int]itemStack{36: {}}, cursor: stack(t, "minecraft:stone", 4)}
	if !honest.matches(before, v) {
		t.Fatal("the vanilla prediction should match")
	}
	forged := clickClaim{slots: map[int]itemStack{36: {}}, cursor: stack(t, "minecraft:stone", 64)}
	if forged.matches(before, v) {
		t.Fatal("a forged cursor should not match")
	}
	silent := clickClaim{cursor: stack(t, "minecraft:stone", 4)}
	if silent.matches(before, v) {
		t.Fatal("an unreported changed slot should not match")
	}
	enchanted := clickClaim{slots: map[int]itemStack{36: {}}, cursor: itemStack{
		ID: itemID(t, "minecraft:stone"), Count: 4, Enchantments: map[string]int{"minecraft:sharpness": 255}}}
	if enchanted.matches(before, v) {
		t.Fatal("client-side enchantments should not match")
	}
}

// totalItems counts every unit in the view: slots, cursor, off hand, drops.
func totalItems(v *clickView) int {
	n := int(v.cursor.Count) + int(v.offhand.Count)
	for _, st := range v.slots {
		n += int(st.Count)
	}
	for _, st := range v.drops {
		n += int(st.Count)
	}
	return n
}

// TestClickNeverCreatesItems fuzzes random clicks (any slot, button, mode)
// over both window kinds: outside creative the number of items — in slots,
// on the cursor, in the off hand or thrown — never changes.
func TestClickNeverCreatesItems(t *testing.T) {
	items := []itemStack{
		stack(t, "minecraft:stone", 1), stack(t, "minecraft:ender_pearl", 1),
		stack(t, "minecraft:iron_helmet", 1), stack(t, "minecraft:diamond", 1),
		stack(t, "minecraft:shield", 1),
	}
	rng := rand.New(rand.NewSource(1))
	for round := 0; round < 300; round++ {
		var v *clickView
		if round%2 == 0 {
			v = invView()
		} else {
			v = chestView()
		}
		for i := range v.slots {
			if rng.Intn(3) == 0 {
				st := items[rng.Intn(len(items))]
				v.slots[i] = withCount(st, 1+rng.Intn(maxStackFor(st.ID)))
			}
		}
		if !v.chest {
			v.slots[craftResultSlot] = itemStack{}
		}
		want := totalItems(v)
		var d dragState
		for step := 0; step < 200; step++ {
			slot := rng.Intn(len(v.slots)+2) - 1
			if slot == -1 && rng.Intn(2) == 0 {
				slot = slotOutside
			}
			button := rng.Intn(11)
			if rng.Intn(10) == 0 {
				button = offhandSwapKey
			}
			v.click(slot, button, rng.Intn(7), &d)
			if got := totalItems(v); got != want {
				t.Fatalf("round %d step %d: %d items, want %d (slot %d button %d)", round, step, got, want, slot, button)
			}
			for i, st := range v.slots {
				if int(st.Count) > maxStackFor(st.ID) {
					t.Fatalf("slot %d over-stacked: %+v", i, st)
				}
			}
		}
	}
}

// TestClickOverSpectatorAndUIItems covers the connection-level guards: a
// hub UI item can't be thrown out of a window.
func TestClickRefusesThrowingUIItems(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Hubber", player.Survival, 0, 64, 0)
	c.inv.set(hotbarStart, itemStack{ID: itemID(t, "minecraft:blaze_rod"), Count: 1, Name: navigatorName})
	clickIn(t, c, 0, int16(hotbarStart), 1, clickThrow)
	if c.inv.get(hotbarStart).Name != navigatorName {
		t.Fatal("the navigator was thrown out of the inventory")
	}
}
