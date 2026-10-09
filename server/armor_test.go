package server

import (
	"math"
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

func wear(c *ClientConnection, slot int16, item string, enchants map[string]int) {
	id, _ := world.ItemByName(item)
	c.inv.set(slot, itemStack{ID: id, Count: 1, Enchantments: enchants})
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 0.01 }

func TestAbsorbDamageVanillaFormula(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Tank", player.Survival, 0, 64, 0)

	if got := c.absorbDamage(7); got != 7 {
		t.Errorf("no armor: %v, want 7", got)
	}

	// Full diamond: 20 points, toughness 8 →
	// 7 × (1 − min(20, max(4, 20 − 7/(2+2))) / 25) = 7 × (1 − 18.25/25) = 1.89
	wear(c, armorHelmetSlot, "minecraft:diamond_helmet", nil)
	wear(c, armorChestplateSlot, "minecraft:diamond_chestplate", nil)
	wear(c, armorLeggingsSlot, "minecraft:diamond_leggings", nil)
	wear(c, armorBootsSlot, "minecraft:diamond_boots", nil)
	if got := c.absorbDamage(7); !near(got, 1.89) {
		t.Errorf("diamond set vs 7: %v, want 1.89", got)
	}

	// Leather set: 7 points, no toughness → max(1.4, 7 − 7/2 = 3.5) → 7 × 0.86 = 6.02
	wear(c, armorHelmetSlot, "minecraft:leather_helmet", nil)
	wear(c, armorChestplateSlot, "minecraft:leather_chestplate", nil)
	wear(c, armorLeggingsSlot, "minecraft:leather_leggings", nil)
	wear(c, armorBootsSlot, "minecraft:leather_boots", nil)
	if got := c.absorbDamage(7); !near(got, 6.02) {
		t.Errorf("leather set vs 7: %v, want 6.02", got)
	}

	// Protection IV on every piece: EPF 16 → another × (1 − 16/25) = 0.36.
	prot := map[string]int{EnchantProtection: 4}
	wear(c, armorHelmetSlot, "minecraft:leather_helmet", prot)
	wear(c, armorChestplateSlot, "minecraft:leather_chestplate", prot)
	wear(c, armorLeggingsSlot, "minecraft:leather_leggings", prot)
	wear(c, armorBootsSlot, "minecraft:leather_boots", prot)
	if got := c.absorbDamage(7); !near(got, 6.02*0.36) {
		t.Errorf("leather + Protection IV vs 7: %v, want %v", got, 6.02*0.36)
	}
}

func TestArmorWearsAndBreaks(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Worn", player.Survival, 0, 64, 0)
	boots, _ := world.ItemByName("minecraft:leather_boots") // durability 65
	c.inv.set(armorBootsSlot, itemStack{ID: boots, Count: 1, Damage: 63})
	wear(c, armorHelmetSlot, "minecraft:iron_helmet", nil)

	c.absorbDamage(8) // wear = max(1, 8/4) = 2 per piece
	if st := c.inv.get(armorBootsSlot); !st.empty() {
		t.Errorf("boots at 63+2 ≥ 65 should break, got %+v", st)
	}
	if st := c.inv.get(armorHelmetSlot); st.Damage != 2 {
		t.Errorf("helmet wear: %d, want 2", st.Damage)
	}
	c.absorbDamage(1)
	if st := c.inv.get(armorHelmetSlot); st.Damage != 3 {
		t.Errorf("small hits wear at least 1: %d, want 3", st.Damage)
	}
}

// duel sets up two live players in a PvP instance at full charge.
func duel(t *testing.T) (*Instance, *ClientConnection, *ClientConnection) {
	t.Helper()
	inst := bareInstance(New(), world.NewMemoryWorld())
	inst.Combat = DefaultCombatConfig()
	inst.combatEnabled.Store(true)
	inst.tickCount.Store(1000) // long since any previous swing → full charge
	a := tntConn(inst, "Attacker", player.Survival, 0, 64, 0)
	v := tntConn(inst, "Victim", player.Survival, 1, 64, 0)
	return inst, a, v
}

func TestWeaponDamageSwitch(t *testing.T) {
	_, a, v := duel(t)
	hold(a, "minecraft:diamond_sword")
	cfg := a.instance.Combat

	// Off (FFA / hub): the config's flat BaseDamage, whatever is held.
	a.handleAttack(v)
	if got := player.MaxHealth - v.player.Health(); got != cfg.BaseDamage {
		t.Errorf("weapon damage off: dealt %v, want BaseDamage %v", got, cfg.BaseDamage)
	}

	// On: the diamond sword's 7, then Sharpness II adds 1.5.
	v.player.SetHealth(player.MaxHealth)
	a.instance.tickCount.Add(1000)
	a.instance.SetWeaponDamage(true)
	a.handleAttack(v)
	if got := player.MaxHealth - v.player.Health(); got != 7 {
		t.Errorf("diamond sword: dealt %v, want 7", got)
	}
	v.player.SetHealth(player.MaxHealth)
	a.instance.tickCount.Add(1000)
	sword, _ := world.ItemByName("minecraft:diamond_sword")
	a.inv.set(hotbarStart, itemStack{ID: sword, Count: 1, Enchantments: map[string]int{EnchantSharpness: 2}})
	a.handleAttack(v)
	if got := player.MaxHealth - v.player.Health(); got != 8.5 {
		t.Errorf("diamond sword + Sharpness II: dealt %v, want 8.5", got)
	}

	// Armor on the victim reduces it.
	v.player.SetHealth(player.MaxHealth)
	a.instance.tickCount.Add(1000)
	wear(v, armorChestplateSlot, "minecraft:iron_chestplate", nil) // 6 points
	a.handleAttack(v)
	// 8.5 × (1 − max(1.2, 6 − 8.5/2 = 1.75)/25) = 8.5 × 0.93 = 7.905
	if got := player.MaxHealth - v.player.Health(); !near(got, 7.905) {
		t.Errorf("vs iron chestplate: dealt %v, want 7.905", got)
	}
}

func TestKnockbackEnchantAddsHorizontalStrength(t *testing.T) {
	_, a, v := duel(t)
	stick, _ := world.ItemByName("minecraft:stick")
	a.inv.set(hotbarStart, itemStack{ID: stick, Count: 1})
	a.handleAttack(v)
	plain := lastPacket(t, v, CbPlayEntityVelocity)

	v.player.SetHealth(player.MaxHealth)
	a.instance.tickCount.Add(1000)
	a.inv.set(hotbarStart, itemStack{ID: stick, Count: 1, Enchantments: map[string]int{EnchantKnockback: 2}})
	a.handleAttack(v)
	boosted := lastPacket(t, v, CbPlayEntityVelocity)
	if plain == nil || boosted == nil {
		t.Fatal("expected velocity packets")
	}
	// Entity Velocity: VarInt eid, then vx/vy/vz as int16 big-endian; the
	// victim is at +X of the attacker, so vx carries the strength.
	vx := func(b []byte) int16 { return int16(uint16(b[len(b)-6])<<8 | uint16(b[len(b)-5])) }
	if vx(boosted) <= vx(plain) {
		t.Errorf("Knockback II should launch harder: plain vx=%d boosted vx=%d", vx(plain), vx(boosted))
	}
}
