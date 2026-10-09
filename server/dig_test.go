package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

func digAction(t *testing.T, cli *testClient, pos world.Position, action int32) {
	t.Helper()
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, action)
	p.Write(protocol.WritePosition(pos.X, pos.Y, pos.Z))
	p.WriteByte(1)
	protocol.WriteVarInt32ToBuffer(&p, 1)
	cli.write(t, SbPlayPlayerAction, p.Bytes())
}

func hold(c *ClientConnection, item string) {
	id, _ := world.ItemByName(item)
	c.inv.set(hotbarStart, itemStack{ID: id, Count: 1})
	c.heldSlot.Store(0)
}

func TestSurvivalDigNeedsTime(t *testing.T) {
	s := New()
	stone := world.Position{X: 5, Y: 70, Z: 5}
	s.Hub.World.SetBlock(stone, world.Stone)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Miner")
	cli.startDiscardDrain()
	c := findConn(t, s, "Miner")
	c.player.SetGamemode(player.Survival)

	// Start + immediate finish: far too early for stone by hand → stays.
	digAction(t, cli, stone, 0)
	digAction(t, cli, stone, 2)
	time.Sleep(100 * time.Millisecond)
	if got := s.Hub.World.GetBlock(stone); got != world.Stone {
		t.Fatalf("stone broke without digging time: %+v", got)
	}

	// Pretend the dig started long ago: the finish is accepted, but stone by
	// hand isn't harvestable → no cobblestone drop.
	digAction(t, cli, stone, 0)
	waitFor(t, time.Second, func() bool { return c.digPos != nil }, "dig to start")
	c.digStartTick = s.Hub.Tick() - 1000
	digAction(t, cli, stone, 2)
	waitFor(t, time.Second, func() bool { return s.Hub.World.GetBlock(stone) == world.Air }, "stone to break")
	if n := len(itemsIn(s.Hub)); n != 0 {
		t.Errorf("stone by hand must not drop, got %v", itemsIn(s.Hub))
	}
}

func TestSurvivalDigWithToolDrops(t *testing.T) {
	s := New()
	stone := world.Position{X: 5, Y: 70, Z: 5}
	s.Hub.World.SetBlock(stone, world.Stone)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Miner")
	cli.startDiscardDrain()
	c := findConn(t, s, "Miner")
	c.player.SetGamemode(player.Survival)
	hold(c, "minecraft:wooden_pickaxe")

	digAction(t, cli, stone, 0)
	waitFor(t, time.Second, func() bool { return c.digPos != nil }, "dig to start")
	c.digStartTick = s.Hub.Tick() - 1000
	digAction(t, cli, stone, 2)
	cobble, _ := world.ItemByName("minecraft:cobblestone")
	waitFor(t, time.Second, func() bool { return itemsIn(s.Hub)[cobble] == 1 }, "cobblestone drop")
}

func TestSurvivalDigFinishesAfterRealTicks(t *testing.T) {
	s := New()
	sandPos := world.Position{X: 5, Y: 70, Z: 5}
	sand, _ := world.BlockByName("minecraft:sand")
	s.Hub.World.SetBlock(sandPos, sand)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Miner")
	cli.startDiscardDrain()
	c := findConn(t, s, "Miner")
	c.player.SetGamemode(player.Survival)
	c.player.MoveTo(0.5, 67, 0.5, true) // on the ground (airborne digs are 5× slower)
	hold(c, "minecraft:diamond_shovel") // sand: 2 ticks

	digAction(t, cli, sandPos, 0)
	time.Sleep(250 * time.Millisecond) // ~5 ticks ≥ the 2 needed
	digAction(t, cli, sandPos, 2)
	waitFor(t, time.Second, func() bool { return s.Hub.World.GetBlock(sandPos) == world.Air }, "sand to break after its dig time")
	sandItem, _ := world.ItemByName("minecraft:sand")
	waitFor(t, time.Second, func() bool { return itemsIn(s.Hub)[sandItem] == 1 }, "sand drop")
}

func TestCreativeBreaksInstantlyWithoutDrop(t *testing.T) {
	s := New()
	pos := world.Position{X: 5, Y: 70, Z: 5}
	s.Hub.World.SetBlock(pos, world.Stone)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Builder")
	cli.startDiscardDrain()
	findConn(t, s, "Builder").player.SetGamemode(player.Creative)

	digAction(t, cli, pos, 0)
	waitFor(t, time.Second, func() bool { return s.Hub.World.GetBlock(pos) == world.Air }, "creative break")
	time.Sleep(50 * time.Millisecond)
	if len(itemsIn(s.Hub)) != 0 {
		t.Error("creative breaks must not drop")
	}
}

func TestBedBreakRemovesPartnerAndDropsOne(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Sleeper")
	cli.startDiscardDrain()
	c := findConn(t, s, "Sleeper")
	c.player.SetGamemode(player.Survival)

	foot := world.Position{X: 5, Y: 70, Z: 5}
	c.placeBed(foot, world.RedBed) // head toward the player's facing (default yaw 0 → +Z)
	head := world.Position{X: 5, Y: 70, Z: 6}
	if s.Hub.World.GetBlock(head) == world.Air {
		t.Fatal("bed head not placed")
	}

	digAction(t, cli, head, 0) // hardness 0.2 by hand = 4 ticks
	waitFor(t, time.Second, func() bool { return c.digPos != nil }, "dig to start")
	c.digStartTick = s.Hub.Tick() - 1000
	digAction(t, cli, head, 2)
	waitFor(t, time.Second, func() bool {
		return s.Hub.World.GetBlock(head) == world.Air && s.Hub.World.GetBlock(foot) == world.Air
	}, "both bed halves to go")
	bedItem, _ := world.ItemByName("minecraft:red_bed")
	waitFor(t, time.Second, func() bool { return itemsIn(s.Hub)[bedItem] == 1 }, "one bed drop")
}

func TestCannotPlaceBlockIntoPlayer(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Placer")
	cli.startDiscardDrain()
	c := findConn(t, s, "Placer")
	c.player.SetGamemode(player.Survival)
	c.player.MoveTo(0.5, 64, 0.5, true)
	hold(c, "minecraft:stone")

	place := func(clicked world.Position, face int32) {
		var p bytes.Buffer
		protocol.WriteVarInt32ToBuffer(&p, 0) // hand
		p.Write(protocol.WritePosition(clicked.X, clicked.Y, clicked.Z))
		protocol.WriteVarInt32ToBuffer(&p, face)
		p.Write(protocol.WriteFloat(0.5))
		p.Write(protocol.WriteFloat(0.5))
		p.Write(protocol.WriteFloat(0.5))
		p.WriteByte(0)
		protocol.WriteVarInt32ToBuffer(&p, 1)
		cli.write(t, SbPlayUseItemOnBlock, p.Bytes())
	}

	// Clicking the top of the block under the feet targets (0,64,0) — the
	// player's own legs. Then (0,65,0) via the face above — the head.
	place(world.Position{X: 0, Y: 63, Z: 0}, 1)
	place(world.Position{X: 0, Y: 64, Z: 0}, 1)
	// A block beside the player is fine.
	place(world.Position{X: 2, Y: 63, Z: 0}, 1)
	waitFor(t, time.Second, func() bool { return s.Hub.World.GetBlock(world.Position{X: 2, Y: 64, Z: 0}) == world.Stone },
		"free block to be placed")
	for _, pos := range []world.Position{{X: 0, Y: 64, Z: 0}, {X: 0, Y: 65, Z: 0}} {
		if got := s.Hub.World.GetBlock(pos); got != world.Air {
			t.Errorf("block placed into the player at %v: %+v", pos, got)
		}
	}
	if !s.Hub.blockedByPlayer(world.Position{X: 0, Y: 65, Z: 0}) || s.Hub.blockedByPlayer(world.Position{X: 0, Y: 66, Z: 0}) {
		t.Error("hitbox should cover y 64..65.8 only")
	}
}
