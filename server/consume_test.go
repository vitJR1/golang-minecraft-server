package server

import (
	"testing"
	"time"

	"minecraft-server/player"
	"minecraft-server/world"
)

func TestGoldenAppleHealsAfterEating(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Eater", player.Survival, 0, 64, 0)
	c.player.SetHealth(10)
	apple, _ := world.ItemByName("minecraft:golden_apple")
	c.inv.set(hotbarStart, itemStack{ID: apple, Count: 2})
	inst.tickCount.Store(100)

	c.startUse("minecraft:golden_apple")
	for tick := uint64(101); tick < 100+consumeTicks; tick++ {
		inst.consumeTick(tick)
	}
	if c.player.Health() != 10 {
		t.Fatal("nothing should happen before the eat time is up")
	}
	inst.consumeTick(100 + consumeTicks)
	if h := c.player.Health(); h != 14 {
		t.Errorf("health after golden apple: %v, want 14", h)
	}
	if c.effectLevel(EffectRegeneration) != 2 {
		t.Error("golden apple should grant Regeneration II")
	}
	if st := c.inv.get(hotbarStart); st.Count != 1 {
		t.Errorf("one apple should be eaten, left %+v", st)
	}
	if c.using.Load() != nil {
		t.Error("use state should be cleared")
	}
}

func TestEatingCancelledBySlotChange(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Fickle", player.Survival, 0, 64, 0)
	c.player.SetHealth(10)
	apple, _ := world.ItemByName("minecraft:golden_apple")
	c.inv.set(hotbarStart, itemStack{ID: apple, Count: 1})
	c.startUse("minecraft:golden_apple")
	c.heldSlot.Store(3)
	for tick := uint64(1); tick <= consumeTicks+5; tick++ {
		inst.consumeTick(tick)
	}
	if c.player.Health() != 10 || c.inv.get(hotbarStart).Count != 1 {
		t.Error("switching slots must abandon the apple")
	}
}

func TestPotionAppliesEffectAndLeavesBottle(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Drinker", player.Survival, 0, 64, 0)
	potion, _ := world.ItemByName("minecraft:potion")
	c.inv.set(hotbarStart, itemStack{ID: potion, Count: 1, Potion: "minecraft:strong_swiftness"})
	c.startUse("minecraft:potion")
	inst.consumeTick(consumeTicks)
	if c.effectLevel(EffectSpeed) != 2 {
		t.Errorf("strong swiftness should give Speed II, got %d", c.effectLevel(EffectSpeed))
	}
	bottle, _ := world.ItemByName("minecraft:glass_bottle")
	if st := c.inv.get(hotbarStart); st.ID != bottle {
		t.Errorf("drinking should leave a glass bottle, got %+v", st)
	}
	if _, _, d, ok := potionEffect("minecraft:invisibility"); !ok || d != 3*time.Minute {
		t.Error("invisibility potion mapping")
	}
}

func TestPearlAndFireChargeAreConsumed(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	c := tntConn(inst, "Thrower", player.Survival, 0.5, 64, 0.5)
	pearl, _ := world.ItemByName("minecraft:ender_pearl")
	c.inv.set(hotbarStart, itemStack{ID: pearl, Count: 3})
	c.throwProjectile("minecraft:ender_pearl")
	c.consumeHeld()
	if st := c.inv.get(hotbarStart); st.Count != 2 {
		t.Errorf("pearl should be spent: %+v", st)
	}
	inst.projMu.Lock()
	n := len(inst.projectiles)
	inst.projMu.Unlock()
	if n != 1 {
		t.Errorf("one pearl in flight, got %d", n)
	}
}

func TestFireballFliesStraightAndExplodes(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	inst.combatEnabled.Store(true)
	c := tntConn(inst, "Mage", player.Survival, 0.5, 64, 0.5)
	c.player.MoveAndLook(0.5, 64, 0.5, 0, 0, true)                      // looking +Z, level
	inst.World.SetBlock(world.Position{X: 0, Y: 65, Z: 6}, world.Stone) // a wall 6 blocks ahead at eye height
	c.throwFireball()
	inst.projMu.Lock()
	p := inst.projectiles[0]
	inst.projMu.Unlock()
	if p.typeID != fireballEntityID || !p.noGravity {
		t.Fatalf("fireball projectile: %+v", p)
	}
	for i := 0; i < 20; i++ {
		inst.projectileTick(uint64(i))
	}
	if lastPacket(t, c, CbPlayExplosion) == nil {
		t.Error("fireball should explode on the wall")
	}
	inst.projMu.Lock()
	n := len(inst.projectiles)
	inst.projMu.Unlock()
	if n != 0 {
		t.Error("fireball should be gone after exploding")
	}
}

func TestSpongeAbsorbsNearbyWater(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	for x := -3; x <= 3; x++ {
		for z := -3; z <= 3; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 64, Z: z}, world.Water)
		}
	}
	far := world.Position{X: 10, Y: 64, Z: 0}
	inst.World.SetBlock(far, world.Water)
	pos := world.Position{X: 0, Y: 64, Z: 0}
	inst.World.SetBlock(pos, world.Sponge)
	if n := inst.absorbWater(pos); n != 48 {
		t.Errorf("absorbed %d, want the 48 connected water blocks", n)
	}
	if inst.World.GetBlock(world.Position{X: 3, Y: 64, Z: 3}) != world.Air {
		t.Error("pool should be dry")
	}
	if inst.World.GetBlock(pos) != world.WetSponge {
		t.Error("sponge should be wet")
	}
	if inst.World.GetBlock(far) != world.Water {
		t.Error("unconnected far water must stay")
	}
}

func TestCustomRespawnKeepsPlayerDeadWithoutDeathScreen(t *testing.T) {
	_, a, v := duel(t)
	a.instance.SetCustomRespawn(true)
	var deaths int
	a.instance.OnPlayerDeath = func(victim, killer *ClientConnection) { deaths++ }
	v.player.SetHealth(1)
	hold(a, "minecraft:diamond_sword")
	a.handleAttack(v)
	if !v.player.IsDead() {
		t.Fatal("victim should be dead")
	}
	if deaths != 1 {
		t.Errorf("OnPlayerDeath fired %d times, want 1", deaths)
	}
	if lastPacket(t, v, CbPlayCombatDeath) != nil {
		t.Error("custom respawn must not show the death screen")
	}
}
