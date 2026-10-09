package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

func TestDrawForceCurve(t *testing.T) {
	if f := drawForce(5); f < 0.18 || f > 0.19 { // t=0.25 → (0.0625+0.5)/3
		t.Errorf("5 ticks: %v", f)
	}
	if f := drawForce(20); f != 1 {
		t.Errorf("20 ticks: %v, want 1 (full)", f)
	}
	if f := drawForce(100); f != 1 {
		t.Errorf("overdraw must cap at 1, got %v", f)
	}
	if arrowDamage(3, 0, false) != 6 || arrowDamage(3, 1, false) != 9 || arrowDamage(3, 0, true) != 9 {
		t.Errorf("damage: %v %v %v", arrowDamage(3, 0, false), arrowDamage(3, 1, false), arrowDamage(3, 0, true))
	}
}

// archery sets up a shooter looking +Z at a target 6 blocks ahead.
func archery(t *testing.T, arrows int) (*Instance, *ClientConnection, *ClientConnection) {
	t.Helper()
	inst, a, v := duel(t)
	a.player.MoveAndLook(0.5, 64, 0.5, 0, 0, true)
	v.player.MoveTo(0.5, 64, 6.5, true)
	hold(a, "minecraft:bow")
	if arrows > 0 {
		id, _ := world.ItemByName("minecraft:arrow")
		a.inv.set(mainInvStart, itemStack{ID: id, Count: byte(arrows)})
	}
	return inst, a, v
}

func fire(inst *Instance, a *ClientConnection, drawTicks uint64) {
	a.startDraw()
	inst.tickCount.Add(drawTicks)
	a.releaseUse()
	for i := 0; i < 40; i++ {
		inst.projectileTick(uint64(i))
	}
}

func TestFullDrawArrowHitsAndConsumesArrow(t *testing.T) {
	inst, a, v := archery(t, 2)
	fire(inst, a, 25)
	// Full draw: 3 blocks/tick, crit → ceil(≈3×2)×1.5 ≈ 9 before drag; 6
	// blocks of flight shave a little speed so 7.5–9 lands.
	if lost := player.MaxHealth - v.player.Health(); lost < 7 || lost > 9 {
		t.Errorf("full-draw crit arrow dealt %v, want 7..9", lost)
	}
	if st := a.inv.get(mainInvStart); st.Count != 1 {
		t.Errorf("one arrow should be spent, left %+v", st)
	}
	if lastPacket(t, v, CbPlayHurtAnimation) == nil {
		t.Error("victim should flash red")
	}
}

func TestShortDrawIsWeakerAndPowerIsStronger(t *testing.T) {
	inst, a, v := archery(t, 5)
	fire(inst, a, 8) // t=0.4 → f=0.32 → ~1 block/tick; may not even reach
	weak := player.MaxHealth - v.player.Health()
	if weak >= 7 {
		t.Errorf("short draw should hit softly or miss, dealt %v", weak)
	}

	v.player.SetHealth(player.MaxHealth)
	inst.tickCount.Add(100)
	bow, _ := world.ItemByName("minecraft:bow")
	a.inv.set(hotbarStart, itemStack{ID: bow, Count: 1, Enchantments: map[string]int{EnchantPower: 2}})
	fire(inst, a, 25)
	if lost := player.MaxHealth - v.player.Health(); lost < 11 {
		t.Errorf("Power II full draw should deal ≥ 11, dealt %v", lost)
	}
}

func TestNoArrowsNoShot(t *testing.T) {
	inst, a, v := archery(t, 0)
	fire(inst, a, 25)
	if v.player.Health() != player.MaxHealth {
		t.Error("shot without arrows")
	}
	inst.projMu.Lock()
	n := len(inst.projectiles)
	inst.projMu.Unlock()
	if n != 0 {
		t.Error("no projectile should exist")
	}
}

func TestArrowSticksInBlockThenVanishes(t *testing.T) {
	inst, a, _ := archery(t, 1)
	inst.World.SetBlock(world.Position{X: 0, Y: 65, Z: 3}, world.Stone) // wall before the target
	a.startDraw()
	inst.tickCount.Add(25)
	a.releaseUse()
	for i := 0; i < 10; i++ {
		inst.projectileTick(uint64(i))
	}
	inst.projMu.Lock()
	if len(inst.projectiles) != 1 || !inst.projectiles[0].stuck {
		inst.projMu.Unlock()
		t.Fatal("arrow should be stuck in the wall")
	}
	inst.projMu.Unlock()
	for i := 0; i <= arrowStuckTicks; i++ {
		inst.projectileTick(uint64(i))
	}
	inst.projMu.Lock()
	n := len(inst.projectiles)
	inst.projMu.Unlock()
	if n != 0 {
		t.Error("stuck arrow should vanish after its time")
	}
}

func TestArrowRespectsAttackVeto(t *testing.T) {
	inst, a, v := archery(t, 1)
	inst.OnPlayerAttack = func(_, _ *ClientConnection) bool { return false } // teammates
	fire(inst, a, 25)
	if v.player.Health() != player.MaxHealth {
		t.Error("vetoed arrow must not hurt")
	}
}
