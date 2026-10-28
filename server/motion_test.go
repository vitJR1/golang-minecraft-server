package server

import (
	"math"
	"testing"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// motion_test.go: movement checks, stage two — speed and flight, with the
// budgets for pushes, effects, ice, ladders and fluids.

// fieldWorld is an offline instance with a wide stone floor at y=63.
func fieldWorld() *Instance {
	inst := bareInstance(New(), world.NewMemoryWorld())
	for x := -5; x <= 60; x++ {
		for z := -5; z <= 5; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	return inst
}

// confirmTeleports answers every pending teleport like a vanilla client.
func confirmTeleports(t *testing.T, c *ClientConnection) {
	t.Helper()
	for _, tp := range c.ac.Teleports.Pending() {
		play(t, c, SbPlayTeleportConfirm, protocol.WriteVarInt32(tp.ID))
	}
}

// path moves c in n equal steps to (x, y, z), confirming setbacks.
func path(t *testing.T, c *ClientConnection, x, y, z float64, n int) {
	t.Helper()
	s := c.player.Snapshot()
	for k := 1; k <= n; k++ {
		f := float64(k) / float64(n)
		setPos(t, c, s.X+(x-s.X)*f, s.Y+(y-s.Y)*f, s.Z+(z-s.Z)*f)
		confirmTeleports(t, c)
	}
}

// --- speed -----------------------------------------------------------------

func TestSprintJumpPaceAllowed(t *testing.T) {
	c := offlineConn(fieldWorld(), "Runner", player.Survival, 0.5, 64, 0.5)
	path(t, c, 12.5, 64, 0.5, 20) // 0.6 blocks a move for a second
	if flaggedNow(c, "movement.speed") || c.player.Snapshot().X != 12.5 {
		t.Fatalf("a sprint-jump pace was refused (x=%v)", c.player.Snapshot().X)
	}
}

func TestSpeedHackRefused(t *testing.T) {
	c := offlineConn(fieldWorld(), "Speeder", player.Survival, 0.5, 64, 0.5)
	path(t, c, 20.5, 64, 0.5, 20) // 1 block a move
	if !flaggedNow(c, "movement.speed") {
		t.Fatal("1 block/tick on foot not flagged")
	}
	if x := c.player.Snapshot().X; x > 12 {
		t.Fatalf("setbacks didn't hold the speeder back: x=%v", x)
	}
}

func TestSpeedEffectAllowsMore(t *testing.T) {
	c := offlineConn(fieldWorld(), "Potion", player.Survival, 0.5, 64, 0.5)
	c.applyEffect(EffectSpeed, 2, time.Minute) // Speed II: ×1.4
	drain(c)
	path(t, c, 20.5, 64, 0.5, 20) // 1 block a move
	if flaggedNow(c, "movement.speed") {
		t.Fatal("Speed II at 1 block/tick flagged")
	}
}

func TestKnockbackAllowsLongMove(t *testing.T) {
	c := offlineConn(fieldWorld(), "Hit", player.Survival, 0.5, 64, 0.5)
	c.giveVelocity(2.0, 0, 0) // no Pong yet: may land on any move
	drain(c)
	setPos(t, c, 2.5, 64, 0.5)
	if flaggedNow(c, "movement.speed") || c.player.Snapshot().X != 2.5 {
		t.Fatal("a knocked-back move was refused")
	}
}

func TestIceAllowsMore(t *testing.T) {
	ice, ok := world.BlockByName("minecraft:blue_ice")
	if !ok {
		t.Skip("blue_ice not registered")
	}
	inst := fieldWorld()
	for x := -5; x <= 60; x++ {
		inst.World.SetBlock(world.Position{X: x, Y: 63, Z: 0}, ice)
	}
	c := offlineConn(inst, "Skater", player.Survival, 0.5, 64, 0.5)
	path(t, c, 30.5, 64, 0.5, 20) // 1.5 blocks a move
	if flaggedNow(c, "movement.speed") {
		t.Fatal("blue-ice speed flagged")
	}
}

func TestCreativeSkipsMotionChecks(t *testing.T) {
	c := offlineConn(fieldWorld(), "Flier", player.Creative, 0.5, 64, 0.5)
	path(t, c, 30.5, 80, 0.5, 20)
	if flaggedNow(c, "movement.speed") || flaggedNow(c, "movement.fly") {
		t.Fatal("creative flight flagged")
	}
}

// --- flight ------------------------------------------------------------------

// jump plays a vanilla jump arc from the ground and back.
func jump(t *testing.T, c *ClientConnection) {
	t.Helper()
	s := c.player.Snapshot()
	y, vy := s.Y, 0.42
	for i := 0; i < 40; i++ {
		y += vy
		vy = (vy - 0.08) * 0.98
		if y <= s.Y {
			setPos(t, c, s.X, s.Y, s.Z)
			return
		}
		setPos(t, c, s.X, y, s.Z)
	}
}

func TestJumpAllowed(t *testing.T) {
	c := offlineConn(fieldWorld(), "Jumper", player.Survival, 0.5, 64, 0.5)
	for i := 0; i < 5; i++ {
		jump(t, c)
	}
	if flaggedNow(c, "movement.fly") {
		t.Fatal("vanilla jumps flagged")
	}
}

func TestFlyingUpRefused(t *testing.T) {
	c := offlineConn(fieldWorld(), "Flier", player.Survival, 0.5, 64, 0.5)
	path(t, c, 0.5, 68, 0.5, 13) // ~0.3 up per move, no jump arc
	if !flaggedNow(c, "movement.fly") {
		t.Fatal("rising 4 blocks not flagged")
	}
	if y := c.player.Snapshot().Y; y > 64+1.6 {
		t.Fatalf("setbacks didn't hold the flier down: y=%v", y)
	}
}

func TestHoverRefused(t *testing.T) {
	c := offlineConn(fieldWorld(), "Hoverer", player.Survival, 0.5, 64, 0.5)
	setPos(t, c, 0.5, 64.42, 0.5)
	setPos(t, c, 0.5, 64.75, 0.5)
	setPos(t, c, 0.5, 65.0, 0.5)
	for i := 0; i < 40; i++ { // and stays there
		setPos(t, c, 0.5+float64(i%2)*0.1, 65.0, 0.5)
		confirmTeleports(t, c)
	}
	if !flaggedNow(c, "movement.fly") {
		t.Fatal("hovering for 2 s not flagged")
	}
}

func TestExplosionLaunchAllowed(t *testing.T) {
	c := offlineConn(fieldWorld(), "Rocket", player.Survival, 0.5, 64, 0.5)
	c.giveVelocity(0, 1.2, 0) // a fireball jump
	drain(c)
	y, vy := 64.0, 1.2
	for vy > 0 {
		y += vy
		vy = (vy - 0.08) * 0.98
		setPos(t, c, 0.5, y, 0.5)
	}
	if flaggedNow(c, "movement.fly") {
		t.Fatalf("a launch to y=%.1f flagged", y)
	}
}

func TestLadderAndWaterExempt(t *testing.T) {
	inst := fieldWorld()
	for y := 64; y <= 72; y++ {
		inst.World.SetBlock(world.Position{X: 2, Y: y, Z: 0}, world.Ladder)
		inst.World.SetBlock(world.Position{X: 6, Y: y, Z: 0}, world.Water)
	}
	climber := offlineConn(inst, "Climber", player.Survival, 2.5, 64, 0.5)
	path(t, climber, 2.5, 70, 0.5, 30) // 0.2 up per move
	swimmer := offlineConn(inst, "Swimmer", player.Survival, 6.5, 64, 0.5)
	path(t, swimmer, 6.5, 70, 0.5, 30)
	if flaggedNow(climber, "movement.fly") || flaggedNow(swimmer, "movement.fly") {
		t.Fatal("climbing a ladder / swimming up flagged")
	}
}

func TestSetbackDoesNotReground(t *testing.T) {
	c := offlineConn(fieldWorld(), "Persistent", player.Survival, 0.5, 64, 0.5)
	path(t, c, 0.5, 66, 0.5, 7) // over the apex → setback in the air
	first := c.ac.Violations.Level("movement.fly", time.Now())
	if first == 0 {
		t.Fatal("setup: not flagged")
	}
	// Keep climbing from where the setback left them: still flagged.
	path(t, c, 0.5, 67, 0.5, 4)
	if c.ac.Violations.Level("movement.fly", time.Now()) <= first {
		t.Fatal("a setback reset the ground and let the flier carry on")
	}
}

func TestTeleportStartsAirStateOver(t *testing.T) {
	c := offlineConn(fieldWorld(), "Pearled", player.Survival, 0.5, 64, 0.5)
	c.player.MoveTo(0.5, 80, 0.5, false)
	_ = c.sendSyncPlayerPosition(0.5, 80, 0.5) // a game teleport into the air
	drain(c)
	confirmTeleports(t, c)
	y, vy := 80.0, 0.0
	for i := 0; i < 10; i++ { // falls normally from there
		vy = (vy - 0.08) * 0.98
		y += vy
		setPos(t, c, 0.5, y, 0.5)
	}
	if flaggedNow(c, "movement.fly") || math.Abs(c.player.Snapshot().Y-y) > 1e-9 {
		t.Fatal("falling after a teleport into the air was refused")
	}
}

// TestSneakBridgingOnEdgeIsGround: hanging over a block's edge by all but a
// hair, standing still for seconds (bridging), is standing — not hovering.
func TestSneakBridgingOnEdgeIsGround(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	inst.World.SetBlock(world.Position{X: 0, Y: 63, Z: 0}, world.Stone)
	// Box spans x 0.9995..1.5995: overlaps the block by 0.0005.
	c := offlineConn(inst, "Bridger", player.Survival, 1.2995, 64, 0.5)
	for i := 0; i < 60; i++ {
		setPos(t, c, 1.2995, 64, 0.5)
	}
	if flaggedNow(c, "movement.fly") {
		t.Fatal("standing on a block's edge flagged as hovering")
	}
}
