package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

func useItem(t *testing.T, cli *testClient) {
	t.Helper()
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0) // hand
	protocol.WriteVarInt32ToBuffer(&p, 1) // sequence
	cli.write(t, SbPlayUseItem, p.Bytes())
}

func TestLavaBucketPlacesLavaAndEmptyBucketScoopsIt(t *testing.T) {
	s := New()
	for x := -2; x <= 2; x++ {
		for z := -2; z <= 2; z++ {
			s.Hub.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Bucketeer")
	cli.startDiscardDrain()
	c := findConn(t, s, "Bucketeer")
	c.player.SetGamemode(player.Survival)
	c.player.MoveAndLook(0.5, 64, 0.5, 0, 90, true) // standing on the floor, looking straight down
	hold(c, bucketLava)

	useItem(t, cli)
	feet := world.Position{X: 0, Y: 64, Z: 0}
	waitFor(t, time.Second, func() bool { return s.Hub.World.GetBlock(feet).Name == world.Lava.Name },
		"lava bucket to place a lava source at the player's feet")
	if got := s.Hub.World.GetBlock(feet); got != world.Lava {
		t.Errorf("placed block must be a lava SOURCE (state %d), got %+v", world.Lava.StateID, got)
	}
	waitFor(t, time.Second, func() bool { return c.heldItemName() == bucketEmpty }, "survival bucket to empty")

	// Scoop it back with the empty bucket.
	useItem(t, cli)
	waitFor(t, time.Second, func() bool { return s.Hub.World.GetBlock(feet) == world.Air }, "empty bucket to scoop the lava")
	waitFor(t, time.Second, func() bool { return c.heldItemName() == bucketLava }, "bucket to hold lava again")

	// Creative keeps the bucket and the fluid is water for a water bucket.
	c.player.SetGamemode(player.Creative)
	hold(c, bucketWater)
	useItem(t, cli)
	waitFor(t, time.Second, func() bool { return s.Hub.World.GetBlock(feet) == world.Water }, "water bucket to place water")
	time.Sleep(50 * time.Millisecond)
	if name := c.heldItemName(); name != bucketWater {
		t.Errorf("creative bucket should stay full, holding %q", name)
	}
}

func TestBucketNeedsATargetAndRespectsVetoes(t *testing.T) {
	s := New()
	SetupHubMenu(s) // hub vetoes placement
	for x := -2; x <= 2; x++ {
		for z := -2; z <= 2; z++ {
			s.Hub.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Dry")
	cli.startDiscardDrain()
	c := findConn(t, s, "Dry")
	c.player.SetGamemode(player.Survival)
	c.player.MoveAndLook(0.5, 64, 0.5, 0, 90, true)
	hold(c, bucketWater)
	useItem(t, cli)
	time.Sleep(80 * time.Millisecond)
	if got := s.Hub.World.GetBlock(world.Position{X: 0, Y: 64, Z: 0}); got != world.Air {
		t.Errorf("hub protection should veto the bucket, got %+v", got)
	}
	if c.heldItemName() != bucketWater {
		t.Error("vetoed bucket must not be consumed")
	}

	// Looking at the sky: nothing solid within reach → nothing happens.
	s.Hub.OnBlockPlace = nil
	c.player.MoveAndLook(0.5, 64, 0.5, 0, -90, true)
	useItem(t, cli)
	time.Sleep(80 * time.Millisecond)
	if c.heldItemName() != bucketWater {
		t.Error("bucket with no target must not be consumed")
	}
}
