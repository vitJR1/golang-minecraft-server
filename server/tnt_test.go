package server

import (
	"bytes"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// tntWorld builds a flat stone floor at y=63 under a 9×9 area around the
// origin so TNT has something to sit on and blow up.
func tntWorld() world.World {
	w := world.NewMemoryWorld()
	for x := -8; x <= 8; x++ {
		for z := -8; z <= 8; z++ {
			w.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	return w
}

// tntConn is an offline connection parked in inst and registered in its
// player list, so explosions see it.
func tntConn(inst *Instance, name string, mode player.Gamemode, x, y, z float64) *ClientConnection {
	c := &ClientConnection{
		server:   inst.Server,
		instance: inst,
		player:   player.New(inst.Server.nextEntityID.Add(1), name, [16]byte{byte(len(name))}),
		outbound: make(chan outboundMsg, 256),
		done:     make(chan struct{}),
		// Plain frames so the test can parse what was sent.
		compressionThreshold: protocol.CompressionDisabled,
	}
	c.player.SetGamemode(mode)
	c.player.MoveTo(x, y, z, true)
	inst.Players.Add(c)
	return c
}

func TestPrimeTNTReplacesBlockWithEntity(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	pos := world.Position{X: 0, Y: 64, Z: 0}
	inst.World.SetBlock(pos, world.TNT)

	if !inst.primeTNT(pos, tntFuseTicks, nil) {
		t.Fatal("primeTNT should accept a TNT block")
	}
	if inst.World.GetBlock(pos) != world.Air {
		t.Error("primed TNT block should be removed")
	}
	if len(inst.tnts) != 1 || inst.tnts[0].fuse != tntFuseTicks {
		t.Fatalf("tnts = %+v", inst.tnts)
	}
	if tt := inst.tnts[0]; tt.x != 0.5 || tt.z != 0.5 || tt.y != 64 || tt.vy != 0.2 {
		t.Errorf("spawn pose: %+v", tt)
	}
	// Not TNT → refused.
	if inst.primeTNT(world.Position{X: 1, Y: 63, Z: 1}, 10, nil) {
		t.Error("stone must not prime")
	}
}

func TestTNTFallsAndRestsOnGround(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	tt := &primedTNT{x: 0.5, y: 68, z: 0.5, fuse: 1000}
	inst.tnts = []*primedTNT{tt}
	for n := 0; n < 60; n++ {
		inst.tntTick(uint64(n))
	}
	if !tt.onGround || tt.y != 64 {
		t.Errorf("TNT should rest on the stone floor at y=64: %+v", tt)
	}
	if tt.fuse != 1000-60 {
		t.Errorf("fuse should count down: %d", tt.fuse)
	}
}

func TestTNTExplosionBreaksBlocksButNotObsidian(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	// A wall of stone next to the TNT, an obsidian block and bedrock too.
	stoneNear := world.Position{X: 1, Y: 64, Z: 0}
	obsidian := world.Position{X: -1, Y: 64, Z: 0}
	bedrock := world.Position{X: 0, Y: 64, Z: 1}
	farStone := world.Position{X: 7, Y: 64, Z: 7} // ~10 blocks away, out of reach
	inst.World.SetBlock(stoneNear, world.Stone)
	inst.World.SetBlock(obsidian, world.Obsidian)
	inst.World.SetBlock(bedrock, world.Bedrock)
	inst.World.SetBlock(farStone, world.Stone)

	inst.tnts = []*primedTNT{{x: 0.5, y: 64, z: 0.5, fuse: 1, onGround: true}}
	inst.tntTick(1)

	if len(inst.tnts) != 0 {
		t.Fatalf("TNT should be gone after exploding: %+v", inst.tnts)
	}
	if inst.World.GetBlock(stoneNear) != world.Air {
		t.Error("adjacent stone should be destroyed")
	}
	if inst.World.GetBlock(world.Position{X: 0, Y: 63, Z: 0}) != world.Air {
		t.Error("floor block under the TNT should be destroyed")
	}
	if inst.World.GetBlock(obsidian) != world.Obsidian {
		t.Error("obsidian must survive a TNT blast")
	}
	if inst.World.GetBlock(bedrock) != world.Bedrock {
		t.Error("bedrock must survive")
	}
	if inst.World.GetBlock(farStone) != world.Stone {
		t.Error("stone 10 blocks away must survive")
	}
}

func TestTNTChainReaction(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	second := world.Position{X: 2, Y: 64, Z: 0}
	inst.World.SetBlock(second, world.TNT)

	inst.tnts = []*primedTNT{{x: 0.5, y: 64, z: 0.5, fuse: 1, onGround: true}}
	inst.tntTick(1)

	if inst.World.GetBlock(second) != world.Air {
		t.Fatal("second TNT block should have been consumed by the blast")
	}
	if len(inst.tnts) != 1 {
		t.Fatalf("second TNT should be primed, tnts = %+v", inst.tnts)
	}
	if f := inst.tnts[0].fuse; f < tntFuseTicks/8 || f >= tntFuseTicks/8+tntFuseTicks/4 {
		t.Errorf("chain fuse %d outside vanilla 10..29", f)
	}
}

func TestTNTExplosionHurtsSurvivalNotCreative(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	inst.combatEnabled.Store(true)
	// 5 blocks out: vanilla deals ~15 of 20 hp there (2 blocks would kill).
	victim := tntConn(inst, "Victim", player.Survival, 5.5, 64, 0.5)
	builder := tntConn(inst, "Builder", player.Creative, -2.5, 64, 0.5)
	far := tntConn(inst, "Far", player.Survival, 20, 64, 0.5)

	inst.explode(0.5, 64.06, 0.5, tntPower, nil)

	if h := victim.player.Snapshot().Health; h >= player.MaxHealth || h <= 0 {
		t.Errorf("survival player 5 blocks away should be hurt but alive, health=%v", h)
	}
	// Point blank is lethal, as in vanilla.
	close := tntConn(inst, "Close", player.Survival, 1.5, 64, 0.5)
	inst.explode(0.5, 64.06, 0.5, tntPower, nil)
	if !close.player.IsDead() {
		t.Errorf("player 1 block from a TNT blast should die, health=%v", close.player.Snapshot().Health)
	}
	if h := builder.player.Snapshot().Health; h != player.MaxHealth {
		t.Errorf("creative player must be immune, health=%v", h)
	}
	if h := far.player.Snapshot().Health; h != player.MaxHealth {
		t.Errorf("player 20 blocks away must be untouched, health=%v", h)
	}

	// The victim's Explosion packet carries their own knockback, pointing
	// away from the blast (+X).
	got := lastPacket(t, victim, CbPlayExplosion)
	if got == nil {
		t.Fatal("victim never received an Explosion packet")
	}
	// The player 20 blocks out is within the 64-block packet range and gets
	// it with zero motion; sounds (16-block range) don't reach them.
	if lastPacket(t, far, CbPlayExplosion) == nil {
		t.Error("player within 64 blocks should receive the Explosion packet")
	}
	if lastPacket(t, far, CbPlaySoundEffect) != nil {
		t.Error("hurt sound should not reach a player 20 blocks away")
	}
	// Skip x,y,z (3 doubles) + strength (float), then records, then motion.
	buf := bytes.NewBuffer(got[8*3+4:])
	n, _ := protocol.ReadVarInt(buf)
	buf.Next(3 * n)
	mx, _ := protocol.ReadFloat(buf)
	if mx <= 0 {
		t.Errorf("knockback should push the victim along +X, got %v", mx)
	}
}

// lastPacket drains c's outbound queue and returns the payload (after the
// packet id) of the last packet with the given id, or nil. Frames must be
// uncompressed (tntConn sets that up).
func lastPacket(t *testing.T, c *ClientConnection, want int32) []byte {
	t.Helper()
	var got []byte
	for len(c.outbound) > 0 {
		msg := <-c.outbound
		if len(msg.frame) == 0 {
			continue
		}
		buf := bytes.NewBuffer(msg.frame)
		if _, err := protocol.ReadVarInt(buf); err != nil { // frame length
			t.Fatal(err)
		}
		id, _ := protocol.ReadVarInt(buf)
		if int32(id) == want {
			got = buf.Bytes()
		}
	}
	return got
}

func TestExplosionPacketSkipsDistantPlayers(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	inst.combatEnabled.Store(true)
	near := tntConn(inst, "Near", player.Survival, 30, 64, 0.5)
	distant := tntConn(inst, "Distant", player.Survival, 100, 64, 0.5)
	inst.explode(0.5, 64.06, 0.5, tntPower, nil)
	if lastPacket(t, near, CbPlayExplosion) == nil {
		t.Error("player 30 blocks away should get the Explosion packet")
	}
	if lastPacket(t, distant, CbPlayExplosion) != nil {
		t.Error("player 100 blocks away should not get the Explosion packet")
	}
}

func TestBroadcastNearFiltersByDistance(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	in := tntConn(inst, "In", player.Survival, 10, 64, 0)
	out := tntConn(inst, "Out", player.Survival, 0, 64, 17)
	inst.playSound("minecraft:entity.tnt.primed", soundCategoryBlocks, 0, 64, 0, 1, 1)
	if lastPacket(t, in, CbPlaySoundEffect) == nil {
		t.Error("player 10 blocks away should hear a volume-1 sound")
	}
	if lastPacket(t, out, CbPlaySoundEffect) != nil {
		t.Error("player 17 blocks away should not be sent a volume-1 sound")
	}
	// Louder sounds carry proportionally further.
	inst.playSound("minecraft:entity.generic.explode", soundCategoryBlocks, 0, 64, 0, 4, 1)
	if lastPacket(t, out, CbPlaySoundEffect) == nil {
		t.Error("volume-4 sound should reach 17 blocks")
	}
}

func TestTNTExplosionNeedsCombat(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	inst.combatEnabled.Store(false)
	victim := tntConn(inst, "Safe", player.Survival, 1.5, 64, 0.5)
	inst.explode(0.5, 64.06, 0.5, tntPower, nil)
	if h := victim.player.Snapshot().Health; h != player.MaxHealth {
		t.Errorf("explosions must not hurt players when combat is off, health=%v", h)
	}
}

func TestFlintAndSteelIgnitesTNT(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	c := tntConn(inst, "Lighter", player.Survival, 3.5, 64, 0.5)
	pos := world.Position{X: 0, Y: 64, Z: 0}
	inst.World.SetBlock(pos, world.TNT)
	flint, _ := world.ItemByName("minecraft:flint_and_steel")
	c.inv.set(hotbarStart, itemStack{ID: flint, Count: 1})
	c.heldSlot.Store(0)

	if !c.tryIgniteTNT(pos, "minecraft:flint_and_steel") {
		t.Fatal("flint and steel on TNT should ignite it")
	}
	if inst.World.GetBlock(pos) != world.Air || len(inst.tnts) != 1 {
		t.Fatalf("TNT should be primed: block=%+v tnts=%d", inst.World.GetBlock(pos), len(inst.tnts))
	}
	if inst.tnts[0].igniter != c {
		t.Error("igniter should be recorded")
	}
	if c.inv.held(0).Damage != 1 {
		t.Errorf("flint and steel should wear one point, damage=%d", c.inv.held(0).Damage)
	}
	// Other items / other blocks do nothing.
	if c.tryIgniteTNT(world.Position{X: 1, Y: 63, Z: 1}, "minecraft:flint_and_steel") {
		t.Error("stone is not ignitable")
	}
	inst.World.SetBlock(pos, world.TNT)
	if c.tryIgniteTNT(pos, "minecraft:stone") {
		t.Error("stone in hand must not ignite TNT")
	}
}

func TestExplosionRecordsAreRelativeOffsets(t *testing.T) {
	recs := explosionRecords(0.5, 64.06, 0.5, []world.Position{{X: 1, Y: 64, Z: 0}, {X: -3, Y: 63, Z: 2}})
	want := []explosionRecord{{1, 0, 0}, {-3, -1, 2}}
	if len(recs) != len(want) {
		t.Fatalf("records = %v", recs)
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Errorf("record %d = %v, want %v", i, recs[i], want[i])
		}
	}
}
