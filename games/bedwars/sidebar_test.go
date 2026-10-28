package bedwars

import (
	"strings"
	"testing"
)

func TestGeneratorTierTimeline(t *testing.T) {
	if genTierAt(Diamond, 0) != 1 || genTierAt(Emerald, 0) != 1 {
		t.Error("both start at tier I")
	}
	if genTierAt(Diamond, 6*60*20) != 2 || genTierAt(Emerald, 6*60*20) != 1 {
		t.Error("6:00 → Diamond II only")
	}
	if genTierAt(Diamond, 18*60*20) != 3 || genTierAt(Emerald, 18*60*20) != 2 {
		t.Error("18:00 → Diamond III, Emerald II")
	}
	if genTierAt(Emerald, 30*60*20) != genMaxTier {
		t.Error("Emerald III by 24:00")
	}
	if u, ok := nextGenUpgrade(0); !ok || u.res != Diamond || u.tier != 2 {
		t.Errorf("first upgrade: %+v", u)
	}
	if _, ok := nextGenUpgrade(24 * 60 * 20); ok {
		t.Error("nothing after Emerald III")
	}
	if clock(7200) != "6:00" || clock(61*20) != "1:01" {
		t.Errorf("clock: %s %s", clock(7200), clock(61*20))
	}
}

func TestSharedGeneratorsSpeedUpWithTier(t *testing.T) {
	g, _, _ := harness(t)
	gen := diamondGenerator(t, g)
	g.mu.Lock()
	base := g.effectiveIntervalLocked(gen)
	g.tick = 6 * 60 * 20
	two := g.effectiveIntervalLocked(gen)
	g.tick = 18 * 60 * 20
	three := g.effectiveIntervalLocked(gen)
	g.mu.Unlock()
	if base != gen.IntervalTicks {
		t.Errorf("tier I interval %d, want the config's %d", base, gen.IntervalTicks)
	}
	if two != uint64(float64(gen.IntervalTicks)*0.75) || three != uint64(float64(gen.IntervalTicks)*0.4) {
		t.Errorf("tier II/III intervals %d/%d for base %d", two, three, gen.IntervalTicks)
	}
}

func TestSidebarCountsKillsAndBeds(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "Red", 1)
	blue := join(g, inst, ctx, "Blue", 2)
	if red.sidebar == nil || !strings.Contains(red.sidebar[0], "Kills: §a0") {
		t.Fatalf("sidebar on join: %v", red.sidebar)
	}
	g.OnBlockBreak(ctx, red, g.arena.BedBlocks[1][0])
	g.OnPlayerDeath(ctx, blue, red)
	g.OnTick(ctx, 20) // sidebar refresh tick
	lines := strings.Join(red.sidebar, "\n")
	if !strings.Contains(lines, "Kills: §a1") || !strings.Contains(lines, "Beds broken: §a1") {
		t.Errorf("red's sidebar after a bed + final kill:\n%s", lines)
	}
	if !strings.Contains(lines, "Diamond: §fTier I") || !strings.Contains(lines, "Emerald: §fTier I") || !strings.Contains(lines, "Next: ") {
		t.Errorf("tier lines missing:\n%s", lines)
	}
	// One line per team: alive with its bed, or out (struck through).
	if !strings.Contains(lines, "§c■ §fRed §a✔ §71/4 §7(you)") || !strings.Contains(lines, "§9■ §7§mBlue§r §c✘ §70/4") ||
		!strings.Contains(lines, "§a■ §fGreen §a✔ §70/4") {
		t.Errorf("team lines:\n%s", lines)
	}
	// The tab list / name tag: "● " in the team colour.
	if tag := red.nameTag; tag.Group != "bw_red" || tag.Prefix != "§c● " || tag.Colour != 'c' {
		t.Errorf("red's name tag %+v", tag)
	}
	// Blue's own board stays at zero.
	if b := strings.Join(blue.sidebar, "\n"); !strings.Contains(b, "Kills: §a0") || !strings.Contains(b, "Beds broken: §a0") {
		t.Errorf("blue's sidebar:\n%s", b)
	}
	g.OnPlayerLeave(ctx, red)
	if red.sidebar != nil {
		t.Error("leaving must clear the sidebar")
	}
}

func TestTierUpgradeIsAnnounced(t *testing.T) {
	g, inst, ctx := harness(t)
	p := join(g, inst, ctx, "Watcher", 1)
	g.OnTick(ctx, 6*60*20)
	inst.mu.Lock()
	chat := strings.Join(inst.broadcasts, "\n")
	inst.mu.Unlock()
	if !strings.Contains(chat, "Diamond Generators") || !strings.Contains(chat, "Tier II") {
		t.Errorf("no upgrade announcement:\n%s", chat)
	}
	if lines := strings.Join(p.sidebar, "\n"); !strings.Contains(lines, "Diamond: §fTier II") {
		t.Errorf("sidebar not at tier II:\n%s", lines)
	}
}
