package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

// digWorld is an offline instance with the given block at (5,70,5) and a
// survival miner standing on a floor at the origin, long since any tick.
func digWorld(t *testing.T, blk world.Block) (*Instance, *ClientConnection, world.Position) {
	t.Helper()
	pos := world.Position{X: 5, Y: 70, Z: 5}
	inst := bareInstance(New(), world.NewMemoryWorld())
	inst.World.SetBlock(pos, blk)
	inst.tickCount.Store(1000)
	c := offlineConn(inst, "Miner", player.Survival, 0.5, 64, 0.5)
	return inst, c, pos
}

func TestSurvivalDigNeedsTime(t *testing.T) {
	inst, c, stone := digWorld(t, world.Stone)

	// Start + immediate finish: far too early for stone by hand → stays.
	digAt(t, c, stone, 0)
	digAt(t, c, stone, 2)
	if got := inst.World.GetBlock(stone); got != world.Stone {
		t.Fatalf("stone broke without digging time: %+v", got)
	}

	// Enough ticks later the finish is accepted, but stone by hand isn't
	// harvestable → no cobblestone drop.
	digAt(t, c, stone, 0)
	if c.digPos == nil {
		t.Fatal("dig should have started")
	}
	inst.tickCount.Add(200) // > 150 ticks by hand
	digAt(t, c, stone, 2)
	if got := inst.World.GetBlock(stone); got != world.Air {
		t.Fatalf("stone should break after its time, got %+v", got)
	}
	if n := len(itemsIn(inst)); n != 0 {
		t.Errorf("stone by hand must not drop, got %v", itemsIn(inst))
	}
}

func TestSurvivalDigWithToolDrops(t *testing.T) {
	inst, c, stone := digWorld(t, world.Stone)
	hold(c, "minecraft:wooden_pickaxe") // 23 ticks

	digAt(t, c, stone, 0)
	inst.tickCount.Add(30)
	digAt(t, c, stone, 2)
	cobble, _ := world.ItemByName("minecraft:cobblestone")
	if itemsIn(inst)[cobble] != 1 {
		t.Errorf("cobblestone drop expected, got %v", itemsIn(inst))
	}
}

func TestSurvivalDigFinishTooEarlyIsRejected(t *testing.T) {
	sand, _ := world.BlockByName("minecraft:sand")
	inst, c, pos := digWorld(t, sand)
	hold(c, "minecraft:diamond_shovel") // sand: 2 ticks → accepted from ceil(0.7·2)=2

	digAt(t, c, pos, 0)
	digAt(t, c, pos, 2) // same tick: too early
	if inst.World.GetBlock(pos) != sand {
		t.Fatal("finish on the start tick must be rejected")
	}
	digAt(t, c, pos, 0)
	inst.tickCount.Add(2)
	digAt(t, c, pos, 2)
	if inst.World.GetBlock(pos) != world.Air {
		t.Error("finish after the dig time should break the block")
	}
	sandItem, _ := world.ItemByName("minecraft:sand")
	if itemsIn(inst)[sandItem] != 1 {
		t.Error("sand should drop")
	}
}

func TestCreativeBreaksInstantlyWithoutDrop(t *testing.T) {
	inst, c, pos := digWorld(t, world.Stone)
	c.player.SetGamemode(player.Creative)
	digAt(t, c, pos, 0)
	if inst.World.GetBlock(pos) != world.Air {
		t.Error("creative start-digging should break at once")
	}
	if len(itemsIn(inst)) != 0 {
		t.Error("creative breaks must not drop")
	}
}

func TestBedBreakRemovesPartnerAndDropsOne(t *testing.T) {
	inst, c, _ := digWorld(t, world.Air)
	foot := world.Position{X: 5, Y: 70, Z: 5}
	c.placeBed(foot, world.RedBed) // head toward the player's facing (yaw 0 → +Z)
	drain(c)
	head := world.Position{X: 5, Y: 70, Z: 6}
	if inst.World.GetBlock(head) == world.Air {
		t.Fatal("bed head not placed")
	}

	digAt(t, c, head, 0) // hardness 0.2 by hand = 4 ticks
	inst.tickCount.Add(10)
	digAt(t, c, head, 2)
	if inst.World.GetBlock(head) != world.Air || inst.World.GetBlock(foot) != world.Air {
		t.Error("both bed halves should go")
	}
	bedItem, _ := world.ItemByName("minecraft:red_bed")
	if itemsIn(inst)[bedItem] != 1 {
		t.Errorf("one bed should drop, got %v", itemsIn(inst))
	}
}

func TestCannotPlaceBlockIntoPlayer(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Placer", player.Survival, 0.5, 64, 0.5)
	stone, _ := world.ItemByName("minecraft:stone")
	c.inv.set(hotbarStart, itemStack{ID: stone, Count: 10})
	c.heldSlot.Store(0)

	// Clicking the top of the block under the feet targets (0,64,0) — the
	// player's own legs. Then (0,65,0) via the face above — the head.
	placeOn(t, c, world.Position{X: 0, Y: 63, Z: 0}, 1)
	placeOn(t, c, world.Position{X: 0, Y: 64, Z: 0}, 1)
	// A block beside the player is fine.
	placeOn(t, c, world.Position{X: 2, Y: 63, Z: 0}, 1)
	if inst.World.GetBlock(world.Position{X: 2, Y: 64, Z: 0}) != world.Stone {
		t.Error("free block should be placed")
	}
	for _, pos := range []world.Position{{X: 0, Y: 64, Z: 0}, {X: 0, Y: 65, Z: 0}} {
		if got := inst.World.GetBlock(pos); got != world.Air {
			t.Errorf("block placed into the player at %v: %+v", pos, got)
		}
	}
	if !inst.blockedByPlayer(world.Position{X: 0, Y: 65, Z: 0}) || inst.blockedByPlayer(world.Position{X: 0, Y: 66, Z: 0}) {
		t.Error("hitbox should cover y 64..65.8 only")
	}
}

// TestPlacingConsumesHeldBlockInSurvival: placing a block takes one from the
// held stack in survival (so place+break doesn't mint blocks) and leaves
// creative stacks alone.
func TestPlacingConsumesHeldBlockInSurvival(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Placer", player.Survival, 0.5, 64, 0.5)
	stoneID, _ := world.ItemByName("minecraft:stone")
	c.inv.set(hotbarStart, itemStack{ID: stoneID, Count: 32})
	c.heldSlot.Store(0)

	placeOn(t, c, world.Position{X: 3, Y: 63, Z: 0}, 1)
	if inst.World.GetBlock(world.Position{X: 3, Y: 64, Z: 0}) != world.Stone {
		t.Fatal("block should be placed")
	}
	if got := c.inv.held(0).Count; got != 31 {
		t.Errorf("held stack after placing: %d, want 31", got)
	}

	// A vetoed placement (into the player) must not consume anything.
	placeOn(t, c, world.Position{X: 0, Y: 63, Z: 0}, 1)
	if got := c.inv.held(0).Count; got != 31 {
		t.Errorf("vetoed placement changed the stack: %d", got)
	}

	// Creative keeps the stack.
	c.player.SetGamemode(player.Creative)
	placeOn(t, c, world.Position{X: 4, Y: 63, Z: 0}, 1)
	if inst.World.GetBlock(world.Position{X: 4, Y: 64, Z: 0}) != world.Stone {
		t.Error("creative block should be placed")
	}
	if got := c.inv.held(0).Count; got != 31 {
		t.Errorf("creative placement consumed the stack: %d", got)
	}
}
