package bedwars

import (
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
)

func TestJoinEquipsStarterKit(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	if sw := red.slot(game.SlotHotbar0); sw.Item != "minecraft:wooden_sword" {
		t.Errorf("hotbar 0 = %+v, want wooden sword", sw)
	}
	team := buildTeams(1)[0]
	for slot, want := range map[int]string{
		game.SlotHelmet: "minecraft:leather_helmet", game.SlotChestplate: "minecraft:leather_chestplate",
		game.SlotLeggings: "minecraft:leather_leggings", game.SlotBoots: "minecraft:leather_boots",
	} {
		st := red.slot(slot)
		if st.Item != want || st.Color != team.Color {
			t.Errorf("slot %d = %+v, want %s dyed %#x", slot, st, want, team.Color)
		}
	}
	if red.Pose().Gamemode != player.Survival {
		t.Error("players join in survival")
	}
}

func TestEquipAppliesTeamUpgrades(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	g.mu.Lock()
	ts := g.teams[0]
	ts.sharpened, ts.protection, ts.haste = true, 2, 1
	k := g.kits[red.eid]
	k.armor, k.pickaxe, k.shears = armorDiamond, 3, true
	g.mu.Unlock()

	g.equip(red, ts, k)
	if sw := red.slot(game.SlotHotbar0); sw.Enchantments["minecraft:sharpness"] != 1 {
		t.Errorf("sword should have Sharpness I: %+v", sw)
	}
	if legs := red.slot(game.SlotLeggings); legs.Item != "minecraft:diamond_leggings" || legs.Enchantments["minecraft:protection"] != 2 {
		t.Errorf("leggings: %+v", legs)
	}
	if helm := red.slot(game.SlotHelmet); helm.Enchantments["minecraft:protection"] != 2 || helm.Color == 0 {
		t.Errorf("helmet keeps dye and gets Protection: %+v", helm)
	}
	if pick := findItem(red, "minecraft:golden_pickaxe"); pick == nil || pick.Enchantments["minecraft:efficiency"] != 3 {
		t.Errorf("tier-3 pickaxe expected: %+v", pick)
	}
	if findItem(red, "minecraft:shears") == nil {
		t.Error("shears expected")
	}
	if red.effect("haste") != 1 {
		t.Errorf("haste I expected, got %d", red.effect("haste"))
	}
}

func TestDeathLootsKillerDowngradesToolsAndRespawnsAfterDelay(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	blue := join(g, inst, ctx, "blue", 2)
	blue.GiveItem("minecraft:iron_ingot", 7)
	blue.GiveItem("minecraft:diamond", 2)
	blue.GiveItem("minecraft:stone_sword", 1)
	g.mu.Lock()
	g.kits[blue.eid].pickaxe = 2
	g.kits[blue.eid].armor = armorIron
	g.mu.Unlock()

	g.OnTick(ctx, 10)
	g.OnPlayerDeath(ctx, blue, red)

	if red.CountItem("minecraft:iron_ingot") != 7 || red.CountItem("minecraft:diamond") != 2 {
		t.Errorf("killer should loot the victim's resources: iron=%d diamond=%d",
			red.CountItem("minecraft:iron_ingot"), red.CountItem("minecraft:diamond"))
	}
	if len(blue.Inventory()) != 0 {
		t.Errorf("victim inventory should be wiped: %+v", blue.Inventory())
	}
	if blue.Pose().Gamemode != player.Spectator {
		t.Error("dead player spectates while waiting")
	}
	if !inst.sawBroadcast("blue was killed by red") {
		t.Errorf("kill message missing: %v", inst.broadcasts)
	}

	// Not yet: still waiting.
	g.OnTick(ctx, 10+respawnDelayTicks-1)
	if blue.respawns != 0 {
		t.Fatal("respawned too early")
	}
	g.OnTick(ctx, 10+respawnDelayTicks)
	if blue.respawns != 1 || blue.Pose().Gamemode != player.Survival {
		t.Fatalf("respawn expected: respawns=%d mode=%v", blue.respawns, blue.Pose().Gamemode)
	}
	spawn := g.arena.Spawns[1].Position
	if int(blue.Pose().X) != spawn.X || int(blue.Pose().Z) != spawn.Z {
		t.Errorf("respawn at team spawn: got (%v,%v) want (%d,%d)", blue.Pose().X, blue.Pose().Z, spawn.X, spawn.Z)
	}
	// Kit after death: iron armour kept, pickaxe down to wood, no stone sword.
	if blue.slot(game.SlotLeggings).Item != "minecraft:iron_leggings" {
		t.Errorf("armour tier should persist: %+v", blue.slot(game.SlotLeggings))
	}
	if findItem(blue, "minecraft:wooden_pickaxe") == nil || findItem(blue, "minecraft:iron_pickaxe") != nil {
		t.Error("pickaxe should drop one tier")
	}
	if findItem(blue, "minecraft:stone_sword") != nil || blue.slot(game.SlotHotbar0).Item != "minecraft:wooden_sword" {
		t.Error("bought swords are lost; wooden sword returns")
	}
}

func TestFriendlyFireVetoed(t *testing.T) {
	g, inst, ctx := harness(t)
	players := []*fakePlayer{}
	for i := 1; i <= 5; i++ {
		players = append(players, join(g, inst, ctx, "p", int32(i)))
	}
	// Players 1 and 5 are both on team 0 (5 joiners over 4 teams).
	if g.OnPlayerAttack(ctx, players[0], players[4]) {
		t.Error("friendly fire must be vetoed")
	}
	if !g.OnPlayerAttack(ctx, players[0], players[1]) {
		t.Error("enemy hits go through to HP combat")
	}
}
