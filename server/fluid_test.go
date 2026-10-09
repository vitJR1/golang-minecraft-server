package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

// fluidWorld builds a stopped instance (manual ticking) with a flat stone
// floor at y=63 covering [-12,12]².
func fluidWorld(t *testing.T) *Instance {
	t.Helper()
	s := New()
	inst := NewInstance("fluids", s, world.NewMemoryWorld())
	inst.Stop() // drive ticks by hand
	for x := -12; x <= 12; x++ {
		for z := -12; z <= 12; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	return inst
}

// runFluid advances the fluid simulation by n water intervals.
func runFluid(inst *Instance, ticks uint64) {
	for t := uint64(1); t <= ticks; t++ {
		inst.fluidTick(t)
	}
}

func levelAt(inst *Instance, x, y, z int) int {
	b := inst.World.GetBlock(world.Position{X: x, Y: y, Z: z})
	k, lvl, ok := fluidOf(b)
	if !ok {
		return -1
	}
	if k == fluidLava {
		return 100 + lvl
	}
	return lvl
}

func TestWaterSpreadsSevenBlocksAndStops(t *testing.T) {
	inst := fluidWorld(t)
	inst.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Water) // source
	runFluid(inst, 200)
	for d := 1; d <= 7; d++ {
		if got := levelAt(inst, d, 64, 0); got != d {
			t.Errorf("distance %d: level %d, want %d", d, got, d)
		}
	}
	if got := levelAt(inst, 8, 64, 0); got != -1 {
		t.Errorf("water must stop after 7 blocks, got level %d at distance 8", got)
	}
	if got := levelAt(inst, 0, 64, 0); got != 0 {
		t.Errorf("source must stay a source, got %d", got)
	}
	if got := levelAt(inst, 0, 65, 0); got != -1 {
		t.Error("water must not flow upward")
	}
}

func TestWaterFallsAndDrains(t *testing.T) {
	inst := fluidWorld(t)
	inst.World.SetBlock(world.Position{X: 3, Y: 63, Z: 0}, world.Air) // a hole in the floor
	inst.World.SetBlock(world.Position{X: 3, Y: 60, Z: 0}, world.Stone)
	inst.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Water)
	// 20 ticks: the flow has reached the hole (x=3) and started falling, but
	// flowing water over a hole only falls — it doesn't continue sideways.
	// (Later it does reach x=4 the long way round through the z=±1 rows,
	// exactly like vanilla.)
	runFluid(inst, 20)
	if got := levelAt(inst, 3, 63, 0); got != fluidFalling {
		t.Errorf("water over the hole should fall (level 8), got %d", got)
	}
	if got := levelAt(inst, 4, 64, 0); got != -1 {
		t.Errorf("flowing water over a hole must not spread past it directly, got %d", got)
	}
	runFluid(inst, 180)
	if got := levelAt(inst, 3, 61, 0); got != fluidFalling {
		t.Errorf("falling column should reach the bottom, got %d", got)
	}
	if got := levelAt(inst, 4, 61, 0); got != 1 {
		t.Errorf("falling water spreads at the bottom with level 1, got %d", got)
	}

	// Remove the source: everything downstream dries up.
	inst.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Air)
	runFluid(inst, 400)
	for _, pos := range []world.Position{{X: 1, Y: 64, Z: 0}, {X: 3, Y: 63, Z: 0}, {X: 3, Y: 61, Z: 0}, {X: 4, Y: 61, Z: 0}} {
		if got := inst.World.GetBlock(pos); got != world.Air {
			t.Errorf("%v should have drained, got %+v", pos, got)
		}
	}
}

func TestTwoSourcesMakeInfiniteWater(t *testing.T) {
	inst := fluidWorld(t)
	inst.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Water)
	inst.SetBlock(world.Position{X: 2, Y: 64, Z: 0}, world.Water)
	runFluid(inst, 50)
	if got := levelAt(inst, 1, 64, 0); got != 0 {
		t.Errorf("block between two sources should become a source, got %d", got)
	}
}

func TestLavaSpreadsShortAndSlow(t *testing.T) {
	inst := fluidWorld(t)
	inst.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Lava)
	runFluid(inst, 29)
	if got := levelAt(inst, 1, 64, 0); got != -1 {
		t.Errorf("lava must not have moved before its 30-tick interval, got %d", got)
	}
	runFluid(inst, 300)
	for d, want := range map[int]int{1: 102, 2: 104, 3: 106} {
		if got := levelAt(inst, d, 64, 0); got != want {
			t.Errorf("lava distance %d: %d, want %d", d, got, want)
		}
	}
	if got := levelAt(inst, 4, 64, 0); got != -1 {
		t.Errorf("lava must stop after 3 blocks, got %d", got)
	}
}

func TestWaterDoesNotReplaceBlocksOrLava(t *testing.T) {
	inst := fluidWorld(t)
	inst.World.SetBlock(world.Position{X: 1, Y: 64, Z: 0}, world.Stone)
	inst.World.SetBlock(world.Position{X: -1, Y: 64, Z: 0}, world.Lava)
	inst.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Water)
	runFluid(inst, 100)
	if inst.World.GetBlock(world.Position{X: 1, Y: 64, Z: 0}) != world.Stone {
		t.Error("water replaced stone")
	}
	if inst.World.GetBlock(world.Position{X: -1, Y: 64, Z: 0}).Name != world.Lava.Name {
		t.Error("water replaced lava")
	}
	if got := levelAt(inst, 0, 64, 1); got != 1 {
		t.Errorf("water should still flow the free way, got %d", got)
	}
}

func TestFluidsCannotBeBroken(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	pos := world.Position{X: 5, Y: 70, Z: 5}
	inst.World.SetBlock(pos, world.Water)
	c := offlineConn(inst, "Puncher", player.Survival, 0.5, 64, 0.5)
	for _, gm := range []player.Gamemode{player.Survival, player.Creative} {
		c.player.SetGamemode(gm)
		digAt(t, c, pos, 0)
		inst.tickCount.Add(1000)
		digAt(t, c, pos, 2)
		if got := inst.World.GetBlock(pos); got != world.Water {
			t.Errorf("gamemode %d: water was broken: %+v", gm, got)
		}
	}
	if world.BreakTicks(world.BreakInfoFor(world.Lava.Name), "minecraft:diamond_pickaxe") != -1 {
		t.Error("lava must be unbreakable in the table")
	}
}

// lavaArena is an offline PvP instance with a lava block at the origin.
func lavaArena(t *testing.T) (*Instance, *ClientConnection) {
	t.Helper()
	inst := bareInstance(New(), world.NewMemoryWorld())
	inst.Combat = DefaultCombatConfig()
	inst.combatEnabled.Store(true)
	inst.World.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Lava)
	c := tntConn(inst, "Swimmer", player.Survival, 0.5, 64, 0.5)
	return inst, c
}

func TestLavaHurtsPlayers(t *testing.T) {
	inst, c := lavaArena(t)
	start := c.player.Health()
	inst.lavaTick(10)
	if h := c.player.Health(); h != start-lavaDamage {
		t.Errorf("health after one lava tick: %v, want %v", h, start-lavaDamage)
	}
	inst.lavaTick(20)
	inst.lavaTick(30)
	if h := c.player.Health(); h != start-3*lavaDamage {
		t.Errorf("health after three lava ticks: %v, want %v", h, start-3*lavaDamage)
	}
	// Out of the lava (and with the fire put out): no more damage.
	// Creative is immune.
	c.player.MoveTo(5.5, 64, 5.5, true)
	c.extinguish()
	inst.lavaTick(40)
	if h := c.player.Health(); h != start-3*lavaDamage {
		t.Error("damage outside lava")
	}
	c.player.MoveTo(0.5, 64, 0.5, true)
	c.player.SetGamemode(player.Creative)
	inst.lavaTick(50)
	if h := c.player.Health(); h != start-3*lavaDamage {
		t.Error("creative player hurt by lava")
	}
}

func TestLavaSetsPlayerOnFireUntilWater(t *testing.T) {
	inst, c := lavaArena(t)
	inst.World.SetBlock(world.Position{X: 9, Y: 64, Z: 9}, world.Water)
	inst.burnTick(10) // in lava: damage + catch fire
	if c.fireTicks.Load() != fireFromLavaTicks {
		t.Fatalf("fire ticks after lava: %d, want %d", c.fireTicks.Load(), fireFromLavaTicks)
	}
	afterLava := c.player.Health()

	// Climb out: the fire keeps burning, 1 damage per 20 ticks. The first
	// fire tick (11) is swallowed by the i-frames of the lava hit at 10, so
	// ticks 31 and 51 land: 2 damage by tick 60.
	c.player.MoveTo(5.5, 64, 5.5, true)
	for tick := uint64(11); tick <= 60; tick++ {
		inst.burnTick(tick)
	}
	if got := c.player.Health(); got != afterLava-2 {
		t.Errorf("fire damage over 50 ticks: health %v, want %v (2 damage)", got, afterLava-2)
	}
	if left := c.fireTicks.Load(); left != fireFromLavaTicks-50 {
		t.Errorf("fire ticks left: %d, want %d", left, fireFromLavaTicks-50)
	}

	// Water puts it out at once.
	c.player.MoveTo(9.5, 64, 9.5, true)
	inst.burnTick(61)
	if c.fireTicks.Load() != 0 {
		t.Error("water should extinguish the fire")
	}
	h := c.player.Health()
	for tick := uint64(62); tick <= 120; tick++ {
		inst.burnTick(tick)
	}
	if c.player.Health() != h {
		t.Error("no damage once extinguished")
	}

	// The fire burns itself out after 300 ticks when nothing puts it out.
	c.setFire(fireFromLavaTicks)
	for tick := uint64(121); tick <= 121+fireFromLavaTicks; tick++ {
		inst.burnTick(tick)
	}
	if c.fireTicks.Load() != 0 {
		t.Errorf("fire should have burnt out, %d ticks left", c.fireTicks.Load())
	}

	// Creative players never catch fire.
	c.player.SetGamemode(player.Creative)
	c.player.MoveTo(0.5, 64, 0.5, true)
	inst.burnTick(500)
	if c.fireTicks.Load() != 0 {
		t.Error("creative player set on fire")
	}
}
