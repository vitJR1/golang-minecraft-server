package server

import (
	"bytes"
	"math"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// movement_test.go: movement checks, stage one — phase, timer, and the
// server-side ground test behind critical hits and the dig penalty.

// setPos sends Set Player Position from an offline connection.
func setPos(t *testing.T, c *ClientConnection, x, y, z float64) {
	t.Helper()
	var p bytes.Buffer
	p.Write(protocol.WriteDouble(x))
	p.Write(protocol.WriteDouble(y))
	p.Write(protocol.WriteDouble(z))
	p.WriteByte(1)
	play(t, c, SbPlaySetPos, p.Bytes())
}

// wallWorld is an offline instance with a floor at y=63 and a one block
// thick, two high stone wall in the x=2 column, z from -2 to 2.
func wallWorld() *Instance {
	inst := bareInstance(New(), world.NewMemoryWorld())
	for x := -3; x <= 6; x++ {
		for z := -3; z <= 3; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	for z := -2; z <= 2; z++ {
		inst.World.SetBlock(world.Position{X: 2, Y: 64, Z: z}, world.Stone)
		inst.World.SetBlock(world.Position{X: 2, Y: 65, Z: z}, world.Stone)
	}
	return inst
}

func TestWalkingIntoWallRefused(t *testing.T) {
	inst := wallWorld()
	c := offlineConn(inst, "Phaser", player.Survival, 1.5, 64, 0.5)
	setPos(t, c, 1.9, 64, 0.5) // box 1.6..2.2 overlaps the wall
	if x := c.player.Snapshot().X; x != 1.5 {
		t.Fatalf("moved into the wall: x=%v", x)
	}
	if !flaggedNow(c, "movement.phase") {
		t.Fatal("not flagged")
	}
}

func TestPhasingThroughWallInOnePacketRefused(t *testing.T) {
	inst := wallWorld()
	// Creative: a 2-block hop would be a speed violation first.
	c := offlineConn(inst, "Phaser", player.Creative, 1.5, 64, 0.5)
	setPos(t, c, 3.5, 64, 0.5) // both ends clear, the wall in between
	if x := c.player.Snapshot().X; x != 1.5 {
		t.Fatalf("went through the wall: x=%v", x)
	}
	if !flaggedNow(c, "movement.phase") {
		t.Fatal("not flagged")
	}
}

func TestWalkingFlushAgainstWallAndOnPartialBlocks(t *testing.T) {
	inst := wallWorld()
	slab, _ := world.BlockByName("minecraft:oak_slab")
	inst.World.SetBlock(world.Position{X: 0, Y: 64, Z: 2}, slab)
	c := offlineConn(inst, "Walker", player.Survival, 1.0, 64, 0.5)
	walk := func(x, y, z float64) { // in walking-speed steps
		s := c.player.Snapshot()
		n := int(math.Ceil(math.Hypot(x-s.X, z-s.Z) / 0.25))
		for k := 1; k <= n; k++ {
			f := float64(k) / float64(n)
			setPos(t, c, s.X+(x-s.X)*f, s.Y+(y-s.Y)*f, s.Z+(z-s.Z)*f)
		}
	}
	walk(1.7, 64, 0.5)   // box max x = 2.0: touching, not inside
	walk(1.7, 64, -1.5)  // along the wall
	walk(0.5, 64, 2.0)   // up to the slab
	walk(0.5, 64.5, 2.5) // standing on it
	if s := c.player.Snapshot(); s.X != 0.5 || s.Y != 64.5 {
		t.Fatalf("legal moves refused: at %v,%v,%v", s.X, s.Y, s.Z)
	}
	if flaggedNow(c, "movement.phase") {
		t.Fatal("legal moves flagged")
	}
}

func TestStuckInsideBlockMayWalkOut(t *testing.T) {
	inst := wallWorld()
	c := offlineConn(inst, "Buried", player.Survival, 2.5, 64, 0.5) // inside the wall
	setPos(t, c, 3.5, 64, 0.5)
	if x := c.player.Snapshot().X; x != 3.5 {
		t.Fatalf("couldn't walk out of a block built around them: x=%v", x)
	}
}

func TestSpectatorPassesWalls(t *testing.T) {
	inst := wallWorld()
	c := offlineConn(inst, "Ghost", player.Spectator, 1.5, 64, 0.5)
	setPos(t, c, 2.5, 64, 0.5)
	if x := c.player.Snapshot().X; x != 2.5 || flaggedNow(c, "movement.phase") {
		t.Fatalf("spectator stopped by a wall: x=%v", x)
	}
}

func TestTimerFlagsPacketBurst(t *testing.T) {
	inst := wallWorld()
	c := offlineConn(inst, "Speedy", player.Survival, 0.5, 64, 0.5)
	var p bytes.Buffer
	p.Write(protocol.WriteFloat(0))
	p.Write(protocol.WriteFloat(0))
	p.WriteByte(1)
	// 20 rotation packets with no time passing: a client ticking flat out.
	for i := 0; i < 20; i++ {
		play(t, c, SbPlaySetRot, p.Bytes())
	}
	if !flaggedNow(c, "movement.timer") {
		t.Fatal("timer not flagged")
	}
}

func TestStatusOnlyMovePacketCounted(t *testing.T) {
	inst := wallWorld()
	c := offlineConn(inst, "Statuser", player.Survival, 0.5, 64, 0.5)
	for i := 0; i < 20; i++ {
		play(t, c, SbPlayMoveStatus, []byte{1})
	}
	if !flaggedNow(c, "movement.timer") {
		t.Fatal("on-ground-only packets escape the timer")
	}
}

// --- server-side ground ---------------------------------------------------

func TestServerGround(t *testing.T) {
	inst := wallWorld()
	c := offlineConn(inst, "Feet", player.Survival, 0.5, 64, 0.5)
	if !c.serverOnGround() {
		t.Fatal("standing on the floor is on ground")
	}
	c.player.MoveTo(0.5, 64.6, 0.5, true) // the packet says ground; it isn't
	if c.serverOnGround() {
		t.Fatal("airborne counted as ground on the client's word")
	}
	c.player.MoveTo(-2.75, 64, 0.5, false) // footprint half over the floor's edge… still on it
	if !c.serverOnGround() {
		t.Fatal("standing on an edge is on ground")
	}
}

// critDamage hits a fresh victim from c and returns the damage dealt.
func critDamage(t *testing.T, inst *Instance, c *ClientConnection, name string) float32 {
	t.Helper()
	s := c.player.Snapshot()
	v := offlineConn(inst, name, player.Survival, s.X+1.5, 64, s.Z)
	before := v.player.Health()
	attack(t, c, v)
	return before - v.player.Health()
}

func TestCriticalHitNeedsRealFall(t *testing.T) {
	inst := pvpInstance()
	for x := -3; x <= 6; x++ {
		for z := -3; z <= 3; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	c := offlineConn(inst, "Critter", player.Survival, 0.5, 64, 0.5)
	inst.Combat.Version = PvP18 // no cooldown: every hit fully charged

	base := critDamage(t, inst, c, "V1")
	if base <= 0 {
		t.Fatal("setup: no damage")
	}
	// On the ground but the packet claims airborne: no crit.
	c.player.MoveTo(0.5, 64, 0.5, false)
	c.ac.Move.LastDY = -0.1
	if got := critDamage(t, inst, c, "V2"); got != base {
		t.Fatalf("grounded 'airborne' hit dealt %v, want %v (no crit)", got, base)
	}
	// Really falling: crit.
	c.player.MoveTo(0.5, 65.2, 0.5, true)
	c.ac.Move.LastDY = -0.15
	if got := critDamage(t, inst, c, "V3"); got <= base {
		t.Fatalf("falling hit dealt %v, want more than %v", got, base)
	}
	// Airborne but rising (jump start): no crit, like vanilla.
	c.ac.Move.LastDY = 0.3
	if got := critDamage(t, inst, c, "V4"); got != base {
		t.Fatalf("rising hit dealt %v, want %v", got, base)
	}
}
