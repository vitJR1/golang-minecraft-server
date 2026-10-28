package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// reach_test.go: gamemode gates and interaction ranges (anticheat.md #7, #8).

// pvpInstance is an offline instance with combat on and the default
// combat tuning, long since any tick.
func pvpInstance() *Instance {
	inst := bareInstance(New(), world.NewMemoryWorld())
	inst.Combat = DefaultCombatConfig()
	inst.SetPvP(true)
	inst.tickCount.Store(1000)
	return inst
}

// attack sends Interact (attack) from c on victim.
func attack(t *testing.T, c, victim *ClientConnection) {
	t.Helper()
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, victim.player.EntityID)
	protocol.WriteVarInt32ToBuffer(&p, 1) // attack
	p.WriteByte(0)                        // sneaking
	play(t, c, SbPlayInteract, p.Bytes())
}

// interactEntity sends Interact (interact, main hand) from c on eid.
func interactEntity(t *testing.T, c *ClientConnection, eid int32) {
	t.Helper()
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, eid)
	protocol.WriteVarInt32ToBuffer(&p, 0) // interact
	protocol.WriteVarInt32ToBuffer(&p, 0) // main hand
	p.WriteByte(0)
	play(t, c, SbPlayInteract, p.Bytes())
}

func hurt(c *ClientConnection) bool { return c.player.Health() < player.MaxHealth }

func flaggedNow(c *ClientConnection, check string) bool {
	return c.ac.Violations.Level(check, time.Now()) > 0
}

// --- #7 melee reach -------------------------------------------------------

func TestAttackWithinReachHits(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Close", player.Survival, 0.5, 64, 0.5)
	v := offlineConn(inst, "Target", player.Survival, 3.0, 64, 0.5) // hitbox face 2.2 away
	attack(t, a, v)
	if !hurt(v) || flaggedNow(a, "combat.reach") {
		t.Fatalf("in-reach hit: hurt=%v flagged=%v", hurt(v), flaggedNow(a, "combat.reach"))
	}
}

func TestAttackBeyondReachCancelledAndFlagged(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Reacher", player.Survival, 0.5, 64, 0.5)
	v := offlineConn(inst, "Far", player.Survival, 5.0, 64, 0.5) // 4.1 to the box
	attack(t, a, v)
	if hurt(v) {
		t.Fatal("a 4-block hit landed")
	}
	if !flaggedNow(a, "combat.reach") {
		t.Fatal("reach not flagged")
	}
}

func TestCreativeAttackReachIsSix(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Builder", player.Creative, 0.5, 64, 0.5)
	v := offlineConn(inst, "Far", player.Survival, 5.0, 64, 0.5)
	attack(t, a, v)
	if flaggedNow(a, "combat.reach") {
		t.Fatal("creative 4-block hit flagged")
	}
}

// TestAttackReachLagCompensated: the victim just stepped back out of reach,
// but the attacker's client (whose newest confirmed tick predates the step)
// still showed it close — the hit counts.
func TestAttackReachLagCompensated(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Laggy", player.Survival, 0.5, 64, 0.5)
	v := offlineConn(inst, "Runner", player.Survival, 5.0, 64, 0.5)
	now := inst.Tick()

	// Attacker confirmed tick now-2; the victim stood close until now-1.
	id, _ := a.ac.Transactions.Begin(now-2, time.Now(), nil)
	if _, err := a.ac.Transactions.Confirm(id, time.Now()); err != nil {
		t.Fatal(err)
	}
	v.ac.History.Record(anticheat.Sample{Tick: now - 2, X: 2.5, Y: 64, Z: 0.5})
	v.ac.History.Record(anticheat.Sample{Tick: now - 1, X: 5.0, Y: 64, Z: 0.5})

	attack(t, a, v)
	if !hurt(v) || flaggedNow(a, "combat.reach") {
		t.Fatalf("lag-compensated hit: hurt=%v flagged=%v", hurt(v), flaggedNow(a, "combat.reach"))
	}
}

// TestAttackReachIgnoresPositionsBeforeConfirmedTick: a close position from
// long before the attacker's confirmed tick no longer counts.
func TestAttackReachIgnoresPositionsBeforeConfirmedTick(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Fresh", player.Survival, 0.5, 64, 0.5)
	v := offlineConn(inst, "Gone", player.Survival, 5.0, 64, 0.5)
	now := inst.Tick()

	id, _ := a.ac.Transactions.Begin(now-1, time.Now(), nil)
	_, _ = a.ac.Transactions.Confirm(id, time.Now())
	v.ac.History.Record(anticheat.Sample{Tick: now - 15, X: 2.5, Y: 64, Z: 0.5}) // long gone
	v.ac.History.Record(anticheat.Sample{Tick: now - 1, X: 5.0, Y: 64, Z: 0.5})

	attack(t, a, v)
	if hurt(v) || !flaggedNow(a, "combat.reach") {
		t.Fatalf("stale position used: hurt=%v flagged=%v", hurt(v), flaggedNow(a, "combat.reach"))
	}
}

// --- #7 block / entity reach ---------------------------------------------

func TestDigOutOfReachRefused(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Nuker", player.Creative, 0.5, 64, 0.5)
	far := world.Position{X: 20, Y: 64, Z: 0}
	inst.World.SetBlock(far, world.Stone)
	digAt(t, c, far, 0)
	if inst.World.GetBlock(far) != world.Stone || !flaggedNow(c, "reach.block") {
		t.Fatal("a 20-block dig went through unflagged")
	}
}

func TestPlaceOutOfReachRefused(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Scaffold", player.Creative, 0.5, 64, 0.5)
	hold(c, "minecraft:stone")
	placeOn(t, c, world.Position{X: 12, Y: 63, Z: 0}, 1)
	if got := inst.World.GetBlock(world.Position{X: 12, Y: 64, Z: 0}); got != world.Air {
		t.Fatalf("placed %s 12 blocks away", got.Name)
	}
	if !flaggedNow(c, "reach.block") {
		t.Fatal("not flagged")
	}
}

func TestPlaceWithinReachWorks(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Mason", player.Creative, 0.5, 64, 0.5)
	hold(c, "minecraft:stone")
	placeOn(t, c, world.Position{X: 3, Y: 63, Z: 0}, 1)
	if got := inst.World.GetBlock(world.Position{X: 3, Y: 64, Z: 0}); got != world.Stone {
		t.Fatalf("in-reach placement failed: %s", got.Name)
	}
}

func TestEntityInteractOutOfReachRefused(t *testing.T) {
	w := world.NewMemoryWorld()
	w.AddEntity(world.Entity{Type: "minecraft:villager", X: 15.5, Y: 64, Z: 0.5})
	s := New()
	inst := NewInstance("npc-reach", s, w)
	t.Cleanup(inst.Stop)
	var calls int
	inst.OnEntityInteract = func(_ *ClientConnection, _ game.EntityInteraction) bool { calls++; return true }
	c := offlineConn(inst, "Shopper", player.Survival, 0.5, 64, 0.5)
	interactEntity(t, c, inst.worldEntities[0].eid)
	if !flaggedNow(c, "reach.entity") || calls != 0 {
		t.Fatalf("15-block NPC click: flagged=%v, shop hook ran %d times", flaggedNow(c, "reach.entity"), calls)
	}
}

// --- #8 gamemode gates ----------------------------------------------------

func TestSpectatorCannotAttack(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Ghost", player.Spectator, 0.5, 64, 0.5)
	v := offlineConn(inst, "Alive", player.Survival, 1.5, 64, 0.5)
	attack(t, a, v)
	if hurt(v) {
		t.Fatal("a spectator dealt damage")
	}
	if flaggedNow(a, "combat.reach") {
		t.Fatal("spectate clicks must not be flagged")
	}
}

func TestDeadPlayerCannotAttack(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Corpse", player.Survival, 0.5, 64, 0.5)
	v := offlineConn(inst, "Alive", player.Survival, 1.5, 64, 0.5)
	a.player.ApplyDamage(100, 1, 0)
	attack(t, a, v)
	if hurt(v) {
		t.Fatal("a dead player dealt damage")
	}
}

func TestAdventureCannotBuild(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Visitor", player.Adventure, 0.5, 64, 0.5)
	stone := world.Position{X: 2, Y: 64, Z: 0}
	inst.World.SetBlock(stone, world.Stone)
	digAt(t, c, stone, 0)
	inst.tickCount.Add(500)
	digAt(t, c, stone, 2)
	if inst.World.GetBlock(stone) != world.Stone {
		t.Fatal("adventure broke a block")
	}
	hold(c, "minecraft:stone")
	placeOn(t, c, world.Position{X: 3, Y: 63, Z: 0}, 1)
	if inst.World.GetBlock(world.Position{X: 3, Y: 64, Z: 0}) != world.Air {
		t.Fatal("adventure placed a block")
	}
}

func TestAdventureCanOpenChest(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Looter", player.Adventure, 0.5, 64, 0.5)
	chest := world.Position{X: 2, Y: 64, Z: 0}
	inst.World.SetBlock(chest, world.Chest)
	placeOn(t, c, chest, 1)
	if m := c.menu.Load(); m == nil || m.kind != "chest" {
		t.Fatal("adventure player couldn't open a chest")
	}
}

func TestSpectatorCannotOpenChestOrDrop(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := offlineConn(inst, "Ghost", player.Spectator, 0.5, 64, 0.5)
	chest := world.Position{X: 2, Y: 64, Z: 0}
	inst.World.SetBlock(chest, world.Chest)
	placeOn(t, c, chest, 1)
	if c.menu.Load() != nil {
		t.Fatal("spectator opened a chest")
	}
	hold(c, "minecraft:stone")
	dropAction(t, c, 4)
	if len(itemsIn(inst)) != 0 {
		t.Fatal("spectator dropped an item")
	}
}
