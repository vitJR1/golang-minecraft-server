package bedwars

import (
	"strings"
	"testing"

	"minecraft-server/game"
)

// openUpgradesFor joins red and opens the team-upgrade menu.
func openUpgradesFor(t *testing.T) (*bedWars, *fakeInstance, *game.Ctx, *fakePlayer) {
	t.Helper()
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	g.openUpgradeShop(ctx, red)
	if red.menuTitle != "Team Upgrades" || red.menuClick == nil {
		t.Fatalf("upgrade menu not opened: %q", red.menuTitle)
	}
	return g, inst, ctx, red
}

func TestUpgradeSharpenedReenchantsExistingSwords(t *testing.T) {
	g, inst, ctx, red := openUpgradesFor(t)
	for i := int32(2); i <= 4; i++ {
		join(g, inst, ctx, "filler", i) // teams 1..3
	}
	mate := join(g, inst, ctx, "mate", 5) // 5th joiner → back to team 0
	mate.GiveStack(game.ItemStack{Item: "minecraft:iron_sword", Count: 1})

	red.menuClick(slotSharpened) // 4 diamonds, has 0 → refused
	if g.teams[0].sharpened {
		t.Fatal("must not unlock without diamonds")
	}
	red.GiveItem("minecraft:diamond", 4)
	red.menuClick(slotSharpened)
	if !g.teams[0].sharpened || red.CountItem("minecraft:diamond") != 0 {
		t.Fatal("Sharpened Swords should be bought for 4 diamonds")
	}
	for _, p := range []*fakePlayer{red, mate} {
		if sw := p.slot(game.SlotHotbar0); sw.Enchantments["minecraft:sharpness"] != 1 {
			t.Errorf("%s wooden sword lacks Sharpness: %+v", p.name, sw)
		}
	}
	if iron := findItem(mate, "minecraft:iron_sword"); iron == nil || iron.Enchantments["minecraft:sharpness"] != 1 {
		t.Errorf("mate's iron sword should be re-enchanted: %+v", iron)
	}
	if ware := menuAt(red, slotSharpened); ware == nil || !ware.Glint || !strings.Contains(strings.Join(ware.Lore, "\n"), "UNLOCKED") {
		t.Errorf("sharpened entry after purchase: %+v", ware)
	}
}

func TestUpgradeArmorTiersAndManiacMiner(t *testing.T) {
	g, _, _, red := openUpgradesFor(t)
	red.GiveItem("minecraft:diamond", 2+4+2)
	red.menuClick(slotArmor) // Protection I, 2 diamonds
	red.menuClick(slotArmor) // Protection II, 4 diamonds
	if g.teams[0].protection != 2 {
		t.Fatalf("protection tier = %d, want 2", g.teams[0].protection)
	}
	if helm := red.slot(game.SlotHelmet); helm.Enchantments["minecraft:protection"] != 2 || helm.Color == 0 {
		t.Errorf("helmet should carry Protection II and keep its dye: %+v", helm)
	}
	red.menuClick(slotMiner) // Haste I, 2 diamonds
	if g.teams[0].haste != 1 || red.effect("haste") != 1 {
		t.Errorf("Maniac Miner I: tier=%d effect=%d", g.teams[0].haste, red.effect("haste"))
	}
	if red.CountItem("minecraft:diamond") != 0 {
		t.Errorf("diamonds left: %d", red.CountItem("minecraft:diamond"))
	}
	red.menuClick(slotMiner) // Haste II costs 4 — broke
	if g.teams[0].haste != 1 {
		t.Error("must not upgrade without diamonds")
	}
}

func TestForgeSpeedsOwnGeneratorsOnlyThisRound(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	gen := redIronGenerator(t, g, red)
	other := newBedWars(g.arena, buildTeams(4), 4) // another round on the same arena

	g.mu.Lock()
	g.teams[0].forge = 2 // +100% → interval halved
	g.mu.Unlock()
	half := gen.IntervalTicks / 2
	g.OnTick(ctx, half)
	if drops := inst.dropsAt(gen.dropPoint()); len(drops) != 1 {
		t.Fatalf("forge II generator should fire at half interval: %d drops", len(drops))
	}
	// The sibling round is untouched.
	otherInst := newFakeInstance()
	otherCtx := &game.Ctx{InstanceID: "other", Instance: otherInst}
	other.OnInstanceStart(otherCtx)
	other.OnTick(otherCtx, half)
	if drops := otherInst.dropsAt(gen.dropPoint()); len(drops) != 0 {
		t.Errorf("a different round must keep the base interval, got %d drops", len(drops))
	}
	// Tier III: emeralds appear at the team generator every 60 s.
	g.mu.Lock()
	g.teams[0].forge = 3
	g.mu.Unlock()
	g.OnTick(ctx, forgeEmeraldInterval[3])
	emeralds := 0
	for _, d := range inst.dropsAt(gen.dropPoint()) {
		if d.item == "minecraft:emerald" {
			emeralds += d.count
		}
	}
	if emeralds != 1 {
		t.Errorf("tier-III forge should drop one emerald, got %d", emeralds)
	}
}

func TestHealPoolRegeneratesOnlyAtBase(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	blue := join(g, inst, ctx, "blue", 2)
	g.mu.Lock()
	g.teams[0].healPool = true
	g.mu.Unlock()
	spawn := g.arena.Spawns[0].Position
	red.Teleport(float64(spawn.X)+0.5, float64(spawn.Y), float64(spawn.Z)+0.5)
	blue.Teleport(float64(spawn.X)+0.5, float64(spawn.Y), float64(spawn.Z)+0.5) // enemy standing in red's base

	g.OnTick(ctx, healPoolInterval)
	if red.effect("regeneration") != 1 {
		t.Error("red should regenerate in their own base")
	}
	if blue.effect("regeneration") != 0 {
		t.Error("heal pool must not heal enemies")
	}
	red.Teleport(float64(spawn.X)+baseRadius+5, float64(spawn.Y), float64(spawn.Z))
	red.RemoveEffect("regeneration")
	g.OnTick(ctx, 2*healPoolInterval)
	if red.effect("regeneration") != 0 {
		t.Error("heal pool only works near the base")
	}
}

func TestTrapsFireOnEntryInQueueOrder(t *testing.T) {
	g, inst, ctx, red := openUpgradesFor(t)
	blue := join(g, inst, ctx, "blue", 2)
	red.GiveItem("minecraft:diamond", 1+2+4+8)
	red.menuClick(slotTrapFirst + int(trapBlind))   // 1
	red.menuClick(slotTrapFirst + int(trapFatigue)) // 2
	red.menuClick(slotTrapFirst + int(trapAlarm))   // 4
	red.menuClick(slotTrapFirst + int(trapCounter)) // queue full → refused
	if n := len(g.teams[0].traps); n != 3 {
		t.Fatalf("queued traps = %d, want 3", n)
	}
	if red.CountItem("minecraft:diamond") != 8 {
		t.Errorf("diamonds left: %d, want 8 (queue full → no charge)", red.CountItem("minecraft:diamond"))
	}
	if q := menuAt(red, slotQueueFirst); q == nil || !strings.Contains(q.Name, "It's a Trap") {
		t.Errorf("queue slot 1: %+v", q)
	}

	// Blue walks into red's base: the first trap fires, blinding + slowing them.
	spawn := g.arena.Spawns[0].Position
	blue.Teleport(float64(spawn.X)+2, float64(spawn.Y), float64(spawn.Z))
	g.OnTick(ctx, trapScanInterval)
	if blue.effect("blindness") != 1 || blue.effect("slowness") != 1 {
		t.Fatalf("It's a Trap! should blind + slow: blind=%d slow=%d", blue.effect("blindness"), blue.effect("slowness"))
	}
	if len(red.titles) == 0 || !strings.Contains(red.titles[len(red.titles)-1], "TRAP TRIGGERED") {
		t.Errorf("defenders should see the alert title: %v", red.titles)
	}
	if n := len(g.teams[0].traps); n != 2 {
		t.Fatalf("trap should be consumed: %d left", n)
	}
	// Staying inside doesn't fire the next one; leaving and re-entering does.
	g.OnTick(ctx, 2*trapScanInterval)
	if blue.effect("mining_fatigue") != 0 {
		t.Error("second trap must not fire while the intruder stays inside")
	}
	blue.Teleport(float64(spawn.X)+baseRadius+10, float64(spawn.Y), float64(spawn.Z))
	g.OnTick(ctx, 3*trapScanInterval)
	blue.Teleport(float64(spawn.X)+2, float64(spawn.Y), float64(spawn.Z))
	g.OnTick(ctx, 4*trapScanInterval)
	if blue.effect("mining_fatigue") != 1 {
		t.Error("Miner Fatigue trap should fire on re-entry")
	}
	// Alarm strips invisibility.
	blue.ApplyEffect("invisibility", 1, 0)
	blue.Teleport(float64(spawn.X)+baseRadius+10, float64(spawn.Y), float64(spawn.Z))
	g.OnTick(ctx, 5*trapScanInterval)
	blue.Teleport(float64(spawn.X)+2, float64(spawn.Y), float64(spawn.Z))
	g.OnTick(ctx, 6*trapScanInterval)
	if blue.effect("invisibility") != 0 {
		t.Error("Alarm trap should reveal the intruder")
	}
	if len(g.teams[0].traps) != 0 {
		t.Error("queue should be empty")
	}
}

func TestCounterOffensiveBoostsDefendersAtBase(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	blue := join(g, inst, ctx, "blue", 2)
	g.mu.Lock()
	g.teams[0].traps = []trapKind{trapCounter}
	g.mu.Unlock()
	spawn := g.arena.Spawns[0].Position
	red.Teleport(float64(spawn.X)+1, float64(spawn.Y), float64(spawn.Z))
	blue.Teleport(float64(spawn.X)+2, float64(spawn.Y), float64(spawn.Z))
	g.OnTick(ctx, trapScanInterval)
	if red.effect("speed") != 2 || red.effect("jump_boost") != 2 {
		t.Errorf("defender should get Speed II + Jump Boost II: speed=%d jump=%d", red.effect("speed"), red.effect("jump_boost"))
	}
	if blue.effect("speed") != 0 {
		t.Error("intruder gets nothing from Counter-Offensive")
	}
}
