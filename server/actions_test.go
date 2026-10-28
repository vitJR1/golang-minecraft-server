package server

import (
	"bytes"
	"testing"

	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// actions_test.go: anticheat.md #10–#13 — fastbreak / nuker, fastplace /
// scaffold, killaura / autoclicker, anti-knockback.

// torch breaks instantly (hardness 0): one "start digging" breaks it.
var torch = world.Block{StateID: 4000, Name: "minecraft:torch"}

// look sets c's view (and the one before it).
func look(c *ClientConnection, yaw, pitch float32) {
	c.player.LookAt(yaw, pitch, true)
	c.ac.Move.PrevYaw, c.ac.Move.PrevPitch = yaw, pitch
}

// --- #10 ------------------------------------------------------------------

func TestNukerCapped(t *testing.T) {
	inst := fieldWorld()
	c := offlineConn(inst, "Nuker", player.Survival, 0.5, 64, 0.5)
	look(c, 0, 30) // ahead (+Z), a little down
	var patch []world.Position
	for x := -1; x <= 1; x++ {
		for z := 2; z <= 5; z++ {
			p := world.Position{X: x, Y: 64, Z: z}
			inst.World.SetBlock(p, torch)
			patch = append(patch, p)
		}
	}
	for _, p := range patch { // all in one instant
		digAt(t, c, p, 0)
	}
	broken := 0
	for _, p := range patch {
		if inst.World.GetBlock(p) == world.Air {
			broken++
		}
	}
	if broken > int(anticheat.BreakBurst)+1 || !flaggedNow(c, "world.fastbreak") {
		t.Fatalf("nuker broke %d of %d at once (flagged=%v)", broken, len(patch), flaggedNow(c, "world.fastbreak"))
	}
}

func TestDigBehindRefused(t *testing.T) {
	inst := fieldWorld()
	c := offlineConn(inst, "Backhand", player.Survival, 0.5, 64, 0.5)
	look(c, 0, 0) // facing +Z
	behind := world.Position{X: 0, Y: 64, Z: -3}
	inst.World.SetBlock(behind, torch)
	digAt(t, c, behind, 0)
	if inst.World.GetBlock(behind) != torch || !flaggedNow(c, "world.angle") {
		t.Fatal("a block behind the player was dug unflagged")
	}
	// The same block in front breaks.
	ahead := world.Position{X: 0, Y: 64, Z: 3}
	inst.World.SetBlock(ahead, torch)
	digAt(t, c, ahead, 0)
	if inst.World.GetBlock(ahead) != world.Air {
		t.Fatal("a block in front wasn't dug")
	}
}

// --- #11 ------------------------------------------------------------------

// placeWith sends Use Item On Block with an explicit cursor.
func placeWith(t *testing.T, c *ClientConnection, clicked world.Position, face int32, cx, cy, cz float32) {
	t.Helper()
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WritePosition(clicked.X, clicked.Y, clicked.Z))
	protocol.WriteVarInt32ToBuffer(&p, face)
	p.Write(protocol.WriteFloat(cx))
	p.Write(protocol.WriteFloat(cy))
	p.Write(protocol.WriteFloat(cz))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 1)
	play(t, c, SbPlayUseItemOnBlock, p.Bytes())
}

func stoneStack(c *ClientConnection) {
	stone, _ := world.ItemByName("minecraft:stone")
	c.inv.set(hotbarStart, itemStack{ID: stone, Count: 64})
	c.heldSlot.Store(0)
}

func TestFastPlaceCapped(t *testing.T) {
	inst := fieldWorld()
	c := offlineConn(inst, "Spammer", player.Survival, 0.5, 64, 0.5)
	stoneStack(c)
	placed := 0
	for x := -2; x <= 2; x++ {
		for z := 2; z <= 5; z++ {
			placeOn(t, c, world.Position{X: x, Y: 63, Z: z}, 1) // floor tops, all at once
			if inst.World.GetBlock(world.Position{X: x, Y: 64, Z: z}) == world.Stone {
				placed++
			}
		}
	}
	if placed > int(anticheat.PlaceBurst)+1 || !flaggedNow(c, "world.fastplace") {
		t.Fatalf("placed %d blocks at once (flagged=%v)", placed, flaggedNow(c, "world.fastplace"))
	}
}

func TestScaffoldHiddenFaceRefused(t *testing.T) {
	inst := fieldWorld()
	c := offlineConn(inst, "Scaffold", player.Survival, 0.5, 64, 0.5)
	stoneStack(c)
	// The east face (+X, 5) of the floor block at x=2 faces away from a
	// player standing west of it.
	placeOn(t, c, world.Position{X: 2, Y: 63, Z: 0}, 5)
	if inst.World.GetBlock(world.Position{X: 3, Y: 63, Z: 0}) == world.Stone && !flaggedNow(c, "world.scaffold") {
		t.Fatal("placed against a hidden face unflagged")
	}
	if !flaggedNow(c, "world.scaffold") {
		t.Fatal("hidden face not flagged")
	}
	// Bridging backwards — standing over the edge, clicking the side the
	// player looks back at — is fine.
	c2 := offlineConn(inst, "Bridger", player.Survival, 3.3, 64, 0.5)
	stoneStack(c2)
	inst.World.SetBlock(world.Position{X: 3, Y: 63, Z: 0}, world.Air)
	placeOn(t, c2, world.Position{X: 2, Y: 63, Z: 0}, 5)
	if inst.World.GetBlock(world.Position{X: 3, Y: 63, Z: 0}) != world.Stone || flaggedNow(c2, "world.scaffold") {
		t.Fatal("a legal bridge placement was refused")
	}
}

func TestPlaceCursorOutsideBlockRefused(t *testing.T) {
	inst := fieldWorld()
	c := offlineConn(inst, "Cursor", player.Survival, 0.5, 64, 0.5)
	stoneStack(c)
	placeWith(t, c, world.Position{X: 0, Y: 63, Z: 2}, 1, 0.5, 7, 0.5)
	if inst.World.GetBlock(world.Position{X: 0, Y: 64, Z: 2}) == world.Stone || !flaggedNow(c, "badpackets.place") {
		t.Fatal("a cursor outside the block wasn't refused")
	}
}

// --- #12 ------------------------------------------------------------------

func TestAttackWithoutSwingFlagged(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Aura", player.Survival, 0.5, 64, 0.5)
	v := offlineConn(inst, "Victim", player.Survival, 2.5, 64, 0.5)
	aimAt(a, v)
	attackRaw(t, a, v)                      // no swing follows
	play(t, a, SbPlayMoveStatus, []byte{1}) // the tick ends
	if !flaggedNow(a, "combat.noswing") {
		t.Fatal("an attack without a swing wasn't flagged")
	}
	b := offlineConn(inst, "Honest", player.Survival, 0.5, 64, -2.5)
	attack(t, b, v) // attack + swing
	play(t, b, SbPlayMoveStatus, []byte{1})
	if flaggedNow(b, "combat.noswing") {
		t.Fatal("a vanilla attack + swing was flagged")
	}
}

func TestAttackOutsideViewRefused(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Spinner", player.Survival, 0.5, 64, 0.5)
	v := offlineConn(inst, "Behind", player.Survival, 0.5, 64, 2.5) // +Z
	look(a, 180, 0)                                                 // facing −Z
	attackRaw(t, a, v)
	if hurt(v) || !flaggedNow(a, "combat.aim") {
		t.Fatalf("hit behind the back: hurt=%v flagged=%v", hurt(v), flaggedNow(a, "combat.aim"))
	}
}

func TestAutoclickerCapped(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Clicker", player.Survival, 0.5, 64, 0.5)
	v := offlineConn(inst, "Target", player.Survival, 2.5, 64, 0.5)
	for i := 0; i < 30; i++ { // 30 clicks in one instant
		attack(t, a, v)
	}
	if !flaggedNow(a, "combat.cps") {
		t.Fatal("30 attacks at once not flagged")
	}
}

// --- #13 ------------------------------------------------------------------

// pushAndConfirm gives c a push and answers its Ping like a client that
// processed it.
func pushAndConfirm(t *testing.T, c *ClientConnection, vx, vy, vz float64) {
	t.Helper()
	before := c.ac.Transactions.Outstanding()
	c.giveVelocity(vx, vy, vz)
	drain(c)
	if c.ac.Transactions.Outstanding() != before+1 {
		t.Fatal("no Ping followed the push")
	}
	// Ids count from 1 on a fresh connection.
	play(t, c, SbPlayPong, protocol.WriteInt(int32(before+1)))
	if c.ac.Transactions.Outstanding() != before {
		t.Fatal("Pong didn't settle the Ping")
	}
}

func TestAntiKnockbackFlagged(t *testing.T) {
	c := offlineConn(fieldWorld(), "Anvil", player.Survival, 0.5, 64, 0.5)
	pushAndConfirm(t, c, 0.5, 0.4, 0)
	for i := 0; i < knockMoves; i++ { // doesn't budge
		setPos(t, c, 0.5, 64, 0.5)
	}
	if !flaggedNow(c, "combat.velocity") {
		t.Fatal("ignoring knockback wasn't flagged")
	}
}

func TestKnockbackTakenNotFlagged(t *testing.T) {
	c := offlineConn(fieldWorld(), "Ragdoll", player.Survival, 0.5, 64, 0.5)
	pushAndConfirm(t, c, 0.5, 0.4, 0)
	x, y, vx, vy := 0.5, 64.0, 0.5, 0.4
	for i := 0; i < knockMoves; i++ {
		x += vx
		y = max(64, y+vy)
		vx *= 0.91
		vy = (vy - 0.08) * 0.98
		setPos(t, c, x, y, 0.5)
	}
	if flaggedNow(c, "combat.velocity") {
		t.Fatal("taking the knockback was flagged")
	}
}

func TestKnockbackIntoWallNotFlagged(t *testing.T) {
	inst := fieldWorld()
	for y := 64; y <= 66; y++ {
		inst.World.SetBlock(world.Position{X: 1, Y: y, Z: 0}, world.Stone)
	}
	c := offlineConn(inst, "Pinned", player.Survival, 0.5, 64, 0.5)
	inst.World.SetBlock(world.Position{X: 0, Y: 66, Z: 0}, world.Stone) // and a ceiling
	pushAndConfirm(t, c, 0.5, 0.4, 0)                                   // straight into the wall
	for i := 0; i < knockMoves; i++ {
		setPos(t, c, 0.5, 64, 0.5)
	}
	if flaggedNow(c, "combat.velocity") {
		t.Fatal("a push into a wall under a ceiling was flagged")
	}
}
