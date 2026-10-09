package server

import (
	"strings"
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

func TestBedFacing(t *testing.T) {
	cases := []struct {
		yaw    float32
		idx    int32
		dx, dz int
	}{
		{0, 1, 0, 1},    // south
		{90, 2, -1, 0},  // west
		{180, 0, 0, -1}, // north
		{270, 3, 1, 0},  // east
	}
	for _, tc := range cases {
		idx, dx, dz := bedFacing(tc.yaw)
		if idx != tc.idx || dx != tc.dx || dz != tc.dz {
			t.Errorf("yaw %v: got idx=%d (%d,%d), want idx=%d (%d,%d)",
				tc.yaw, idx, dx, dz, tc.idx, tc.dx, tc.dz)
		}
	}
}

func TestPlaceBedTwoBlocks(t *testing.T) {
	w := world.NewMemoryWorld()
	inst := bareInstance(New(), w)
	c := &ClientConnection{
		instance: inst,
		player:   player.New(1, "P", [16]byte{}),
		outbound: make(chan outboundMsg, 16),
		done:     make(chan struct{}),
	}
	c.player.LookAt(0, 0, false) // facing south (+Z)

	pos := world.Position{X: 5, Y: 64, Z: 5}
	c.placeBed(pos, world.RedBed)

	// red_bed: min = default(1915) - 3 = 1912; facing south = idx 1.
	// foot = 1912 + 1*4 + 3 = 1919; head = 1912 + 1*4 + 2 = 1918.
	foot := w.GetBlock(pos)
	head := w.GetBlock(world.Position{X: 5, Y: 64, Z: 6})
	if foot.StateID != 1919 || foot.Name != "minecraft:red_bed" {
		t.Errorf("foot = %+v, want StateID 1919 red_bed", foot)
	}
	if head.StateID != 1918 || head.Name != "minecraft:red_bed" {
		t.Errorf("head = %+v, want StateID 1918 red_bed", head)
	}
	if foot.StateID == head.StateID {
		t.Error("foot and head must differ (not two identical blocks)")
	}

	// Both halves are registered as bed block entities (so they render).
	be := w.BlockEntities()
	if be[pos] != "minecraft:bed" || be[world.Position{X: 5, Y: 64, Z: 6}] != "minecraft:bed" {
		t.Errorf("bed block entities not registered: %v", be)
	}
}

func TestBedFromItem(t *testing.T) {
	if b, ok := bedFromItem("minecraft:red_bed"); !ok || b != world.RedBed {
		t.Errorf("red_bed: %+v ok=%v", b, ok)
	}
	if _, ok := bedFromItem("minecraft:stone"); ok {
		t.Error("stone is not a bed")
	}
}

// recolourLogic implements game.PlacementRewriter: every bed becomes blue.
type recolourLogic struct{ game.NoopLogic }

func (recolourLogic) RewritePlacedBlock(_ *game.Ctx, _ game.PlayerHandle, _ world.Position, blk world.Block) world.Block {
	if strings.HasSuffix(blk.Name, "_bed") {
		return world.BlueBed
	}
	return blk
}

func TestPlacementRewriterRecoloursBeds(t *testing.T) {
	s := New()
	inst := NewInstance("arena", s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	s.AddInstance(inst)
	s.AttachLogic(inst, recolourLogic{})
	c := offlineConn(inst, "Sleeper", player.Survival, 0.5, 80, 0.5)
	foot := world.Position{X: 5, Y: 70, Z: 5}
	c.placeBed(foot, world.RedBed) // held a red bed, yaw 0 → head at z+1
	head := world.Position{X: 5, Y: 70, Z: 6}
	if got := inst.World.GetBlock(foot); got.Name != world.BlueBed.Name {
		t.Errorf("foot: %+v, want a blue bed", got)
	}
	if got := inst.World.GetBlock(head); got.Name != world.BlueBed.Name {
		t.Errorf("head: %+v, want a blue bed", got)
	}
	if hp := world.StateProperties(world.BlueBed.Name, inst.World.GetBlock(head).StateID); hp["part"] != "head" {
		t.Errorf("head half lost its part: %v", hp)
	}
}
