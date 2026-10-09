package bedwars

import (
	"testing"

	"minecraft-server/game"
	"minecraft-server/world"
)

func TestPotionsApplyByName(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	if !g.OnItemConsume(ctx, red, game.ItemStack{Item: "minecraft:potion", Count: 1, Name: nameSpeedPot}) {
		t.Fatal("shop potion should be handled by the game")
	}
	if red.effect("speed") != 2 {
		t.Errorf("Speed II expected, got %d", red.effect("speed"))
	}
	if g.OnItemConsume(ctx, red, game.ItemStack{Item: "minecraft:golden_apple", Count: 1}) {
		t.Error("golden apple falls through to the vanilla default")
	}
}

func TestBridgeEggLaysTeamWoolUnderItsPath(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	red.SetSlot(game.SlotHotbar0+1, game.ItemStack{Item: "minecraft:egg", Count: 2, Name: nameBridgeEgg})
	red.Teleport(0, 70, 0)

	if g.OnItemUse(ctx, red, game.ItemUse{Item: "minecraft:egg", Name: nameBridgeEgg, Slot: 1}) {
		t.Fatal("bridge egg use should be consumed")
	}
	if st := red.slot(game.SlotHotbar0 + 1); st.Count != 1 {
		t.Errorf("one egg should be used: %+v", st)
	}
	if len(inst.projectiles) != 1 || inst.projectiles[0].item != "minecraft:egg" {
		t.Fatalf("egg projectile expected: %+v", inst.projectiles)
	}
	hooks := inst.projectiles[0].hooks
	// Fly along +X: the first tick only establishes direction, then wool
	// appears two blocks below, three wide across Z.
	hooks.OnTick(1.2, 70, 0)
	hooks.OnTick(2.4, 70, 0)
	for dz := -1; dz <= 1; dz++ {
		if b := inst.GetBlock(world.Position{X: 2, Y: 68, Z: dz}); b != world.RedWool {
			t.Errorf("bridge block at z=%d: %+v", dz, b)
		}
	}
	// Existing blocks are left alone, and the range limit ends the flight.
	inst.SetBlock(world.Position{X: 3, Y: 68, Z: 0}, world.Stone)
	hooks.OnTick(3.6, 70, 0)
	if inst.GetBlock(world.Position{X: 3, Y: 68, Z: 0}) != world.Stone {
		t.Error("bridge must not overwrite blocks")
	}
	if hooks.OnTick(40, 70, 0) {
		t.Error("flight should end past the range limit")
	}
	g.mu.Lock()
	placed := g.placed[world.Position{X: 2, Y: 68, Z: 0}]
	g.mu.Unlock()
	if !placed {
		t.Error("bridge blocks are player-placed (breakable)")
	}
	// Plain eggs pass through to the core.
	if !g.OnItemUse(ctx, red, game.ItemUse{Item: "minecraft:egg", Slot: 1}) {
		t.Error("an unnamed egg is a vanilla throw")
	}
}

func TestPopupTowerBuildsAroundPlayer(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	red.SetSlot(game.SlotHotbar0+2, game.ItemStack{Item: "minecraft:chest", Count: 1, Name: namePopupTower})
	red.Teleport(10.5, 70, 10.5)

	if g.OnItemUse(ctx, red, game.ItemUse{Item: "minecraft:chest", Name: namePopupTower, Slot: 2}) {
		t.Fatal("tower use should be consumed")
	}
	if !red.slot(game.SlotHotbar0 + 2).Empty() {
		t.Error("the tower item should be used up")
	}
	// Walls on every level, ladder up the middle, floor below, player on top.
	for dy := 0; dy <= popupTowerHeight; dy++ {
		if b := inst.GetBlock(world.Position{X: 11, Y: 70 + dy, Z: 10}); b != world.RedWool {
			t.Errorf("wall at dy=%d: %+v", dy, b)
		}
	}
	if b := inst.GetBlock(world.Position{X: 10, Y: 72, Z: 10}); b.Name != "minecraft:ladder" {
		t.Errorf("ladder column: %+v", b)
	}
	if b := inst.GetBlock(world.Position{X: 10, Y: 69, Z: 10}); b != world.RedWool {
		t.Errorf("floor: %+v", b)
	}
	if pose := red.Pose(); int(pose.Y) != 70+popupTowerHeight {
		t.Errorf("player should be lifted to the top: y=%v", pose.Y)
	}
	// A blocked footprint refuses and keeps the item.
	red.SetSlot(game.SlotHotbar0+2, game.ItemStack{Item: "minecraft:chest", Count: 1, Name: namePopupTower})
	red.Teleport(10.5, 70, 10.5) // inside the finished tower: walls already there
	g.OnItemUse(ctx, red, game.ItemUse{Item: "minecraft:chest", Name: namePopupTower, Slot: 2})
	if red.slot(game.SlotHotbar0 + 2).Empty() {
		t.Error("a refused tower must not consume the item")
	}
}
