package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

// bucketWorld is a stone floor at y=63 around the origin.
func bucketWorld() world.World {
	w := world.NewMemoryWorld()
	for x := -2; x <= 2; x++ {
		for z := -2; z <= 2; z++ {
			w.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	return w
}

// Offline connections (tntConn) keep these tests single-goroutine: no
// readLoop races with the test writing inventory / position.
func TestLavaBucketPlacesLavaAndEmptyBucketScoopsIt(t *testing.T) {
	inst := bareInstance(New(), bucketWorld())
	c := tntConn(inst, "Bucketeer", player.Survival, 0.5, 64, 0.5)
	c.player.MoveAndLook(0.5, 64, 0.5, 0, 90, true) // standing on the floor, looking straight down
	hold(c, bucketLava)

	c.useBucket(bucketLava)
	feet := world.Position{X: 0, Y: 64, Z: 0}
	if got := inst.World.GetBlock(feet); got != world.Lava {
		t.Fatalf("lava bucket should place a lava SOURCE (state %d) at the feet, got %+v", world.Lava.StateID, got)
	}
	if name := c.heldItemName(); name != bucketEmpty {
		t.Errorf("survival bucket should be empty after use, holding %q", name)
	}

	// Scoop it back with the empty bucket.
	c.useBucket(bucketEmpty)
	if got := inst.World.GetBlock(feet); got != world.Air {
		t.Errorf("empty bucket should scoop the lava, got %+v", got)
	}
	if name := c.heldItemName(); name != bucketLava {
		t.Errorf("bucket should hold lava again, holding %q", name)
	}

	// Creative keeps the bucket, and a water bucket places water.
	c.player.SetGamemode(player.Creative)
	hold(c, bucketWater)
	c.useBucket(bucketWater)
	if got := inst.World.GetBlock(feet); got != world.Water {
		t.Errorf("water bucket should place water, got %+v", got)
	}
	if name := c.heldItemName(); name != bucketWater {
		t.Errorf("creative bucket should stay full, holding %q", name)
	}
}

func TestBucketNeedsATargetAndRespectsVetoes(t *testing.T) {
	inst := bareInstance(New(), bucketWorld())
	inst.OnBlockPlace = func(*ClientConnection, world.Position, world.Block) bool { return false } // protected
	c := tntConn(inst, "Dry", player.Survival, 0.5, 64, 0.5)
	c.player.MoveAndLook(0.5, 64, 0.5, 0, 90, true)
	hold(c, bucketWater)
	c.useBucket(bucketWater)
	if got := inst.World.GetBlock(world.Position{X: 0, Y: 64, Z: 0}); got != world.Air {
		t.Errorf("placement veto should stop the bucket, got %+v", got)
	}
	if c.heldItemName() != bucketWater {
		t.Error("vetoed bucket must not be consumed")
	}

	// Looking at the sky: nothing solid within reach → nothing happens.
	inst.OnBlockPlace = nil
	c.player.MoveAndLook(0.5, 64, 0.5, 0, -90, true)
	c.useBucket(bucketWater)
	if c.heldItemName() != bucketWater {
		t.Error("bucket with no target must not be consumed")
	}
}
