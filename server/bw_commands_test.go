package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/world"
)

func TestBwGiveAndKillOnlyInWeaponDamageInstances(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	inst.Combat = DefaultCombatConfig()
	inst.combatEnabled.Store(true)
	c := tntConn(inst, "QA", player.Survival, 0, 64, 0)
	inst.Server.Ops.Add("QA")
	iron, _ := world.ItemByName("minecraft:iron_ingot")

	// Outside an arena: refused.
	cmdBwGive(c, []string{"iron", "10"})
	if c.countItem(iron) != 0 {
		t.Error("bwgive must be refused where weapon damage is off")
	}
	cmdBwKill(c, nil)
	if c.player.IsDead() {
		t.Error("bwkill must be refused where weapon damage is off")
	}

	inst.SetWeaponDamage(true)
	cmdBwGive(c, []string{"IRON", "10"})
	if got := c.countItem(iron); got != 10 {
		t.Errorf("iron after bwgive: %d, want 10", got)
	}
	cmdBwGive(c, []string{"emerald"})
	emerald, _ := world.ItemByName("minecraft:emerald")
	if got := c.countItem(emerald); got != 16 {
		t.Errorf("default amount: %d, want 16", got)
	}
	cmdBwGive(c, []string{"netherite", "1"})
	cmdBwGive(c, []string{"gold", "0"})
	gold, _ := world.ItemByName("minecraft:gold_ingot")
	if c.countItem(gold) != 0 {
		t.Error("bad amount must give nothing")
	}

	// Kill goes through die(): with armour on it's still lethal.
	wear(c, armorChestplateSlot, "minecraft:diamond_chestplate", nil)
	var deaths int
	inst.OnPlayerDeath = func(_, _ *ClientConnection) { deaths++ }
	cmdBwKill(c, nil)
	if !c.player.IsDead() || deaths != 1 {
		t.Errorf("bwkill: dead=%v deaths=%d", c.player.IsDead(), deaths)
	}
}
