package server

import (
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// explosionLoss is the health a survival player standing 5 blocks from a
// TNT blast loses in a fresh instance with the given damage multiplier.
func explosionLoss(t *testing.T, scale float64) float32 {
	t.Helper()
	inst := bareInstance(New(), tntWorld())
	inst.combatEnabled.Store(true)
	if scale != 1 {
		inst.SetExplosionDamage(scale)
	}
	victim := tntConn(inst, "Victim", player.Survival, 5.5, 64, 0.5)
	inst.explode(0.5, 64.06, 0.5, tntPower, nil)
	return player.MaxHealth - victim.player.Health()
}

// TestExplosionDamageScale: SetExplosionDamage multiplies what a blast does
// to players — 0.5 halves it, 0 leaves health alone — and defaults to 1.
func TestExplosionDamageScale(t *testing.T) {
	full := explosionLoss(t, 1)
	if full <= 0 {
		t.Fatalf("a survival player 5 blocks out should be hurt, lost %v", full)
	}
	if half := explosionLoss(t, 0.5); half != full/2 {
		t.Errorf("half scale: lost %v, want %v", half, full/2)
	}
	if none := explosionLoss(t, 0); none != 0 {
		t.Errorf("zero scale should not hurt, lost %v", none)
	}
	inst := bareInstance(New(), tntWorld())
	if inst.ExplosionDamage() != 1 {
		t.Errorf("default multiplier %v, want 1", inst.ExplosionDamage())
	}
	inst.SetExplosionDamage(-3)
	if inst.ExplosionDamage() != 0 {
		t.Errorf("negative scale should clamp to 0, got %v", inst.ExplosionDamage())
	}
}

// shieldingLogic vetoes explosions at one position (game.ExplosionBreakVeto)
// while allowing every ordinary block break.
type shieldingLogic struct {
	game.NoopLogic
	shielded world.Position
	igniters []game.PlayerHandle
}

func (l *shieldingLogic) OnExplosionBreak(_ *game.Ctx, igniter game.PlayerHandle, pos world.Position) bool {
	l.igniters = append(l.igniters, igniter)
	return pos != l.shielded
}

// TestExplosionBreakVetoKeepsBlock: a Logic implementing
// game.ExplosionBreakVeto keeps its block through a blast while the stone
// next to it still goes; the hook sees the igniter (nil when unknown).
func TestExplosionBreakVetoKeepsBlock(t *testing.T) {
	s := New()
	inst := bareInstance(s, tntWorld())
	shielded := world.Position{X: 1, Y: 64, Z: 0}
	plain := world.Position{X: -1, Y: 64, Z: 0}
	inst.World.SetBlock(shielded, world.Stone)
	inst.World.SetBlock(plain, world.Stone)
	logic := &shieldingLogic{shielded: shielded}
	s.AttachLogic(inst, logic)

	igniter := tntConn(inst, "Igniter", player.Creative, 10, 64, 10)
	inst.explode(0.5, 64.06, 0.5, tntPower, igniter)

	if inst.World.GetBlock(shielded) != world.Stone {
		t.Error("the vetoed block must survive the blast")
	}
	if inst.World.GetBlock(plain) != world.Air {
		t.Error("an unshielded block next to the TNT should still be destroyed")
	}
	if len(logic.igniters) == 0 {
		t.Fatal("the veto hook never ran")
	}
	for _, ig := range logic.igniters {
		if ig == nil || ig.Name() != "Igniter" {
			t.Fatalf("hook should see the igniter, got %v", ig)
		}
	}

	// Unknown igniter → nil handle, and the veto still applies.
	inst.World.SetBlock(shielded, world.Stone)
	logic.igniters = nil
	inst.explode(0.5, 64.06, 0.5, tntPower, nil)
	if inst.World.GetBlock(shielded) != world.Stone {
		t.Error("the vetoed block must survive an anonymous blast too")
	}
	for _, ig := range logic.igniters {
		if ig != nil {
			t.Fatalf("anonymous blast should pass a nil igniter, got %v", ig)
		}
	}
}

// TestFireballThrownAtOwnFeet: with a block in reach the client sends Use
// Item On Block for a fire charge (it predicts placing fire there); the
// server must still launch the fireball, consume the charge, and roll the
// predicted fire back — this is the Hypixel "fireball jump" at your feet.
func TestFireballThrownAtOwnFeet(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	inst.combatEnabled.Store(true)
	c := offlineConn(inst, "Jumper", player.Survival, 0.5, 64, 0.5)
	c.player.MoveAndLook(0.5, 64, 0.5, 0, 90, true) // looking straight down
	charge, _ := world.ItemByName("minecraft:fire_charge")
	c.inv.set(hotbarStart, itemStack{ID: charge, Count: 2})
	drain(c)

	floor := world.Position{X: 0, Y: 63, Z: 0}
	placeOn(t, c, floor, 1) // top face → the client predicts fire at (0,64,0)

	inst.projMu.Lock()
	n := len(inst.projectiles)
	var item string
	if n > 0 {
		item = inst.projectiles[0].item
	}
	inst.projMu.Unlock()
	if n != 1 || item != "minecraft:fire_charge" {
		t.Fatalf("fireball should be in flight: %d projectiles (%q)", n, item)
	}
	if st := c.inv.get(hotbarStart); st.Count != 1 {
		t.Errorf("one charge should be spent, left %+v", st)
	}
	if inst.World.GetBlock(world.Position{X: 0, Y: 64, Z: 0}) != world.Air {
		t.Error("no fire may be placed")
	}
	for i := 0; i < 5; i++ {
		inst.projectileTick(uint64(i))
	}
	if lastPacket(t, c, CbPlayExplosion) == nil {
		t.Error("the fireball should explode on the floor under the thrower")
	}
	if h := c.player.Health(); h >= player.MaxHealth {
		t.Errorf("a fireball at the feet should hurt the thrower, health %v", h)
	}
}
