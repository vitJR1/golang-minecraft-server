package bedwars

import (
	"minecraft-server/game"
	"strconv"
	"strings"
	"testing"
)

// diamondGenerator returns the arena's (first) diamond generator.
func diamondGenerator(t *testing.T, g *bedWars) Generator {
	t.Helper()
	for _, gen := range g.arena.Generators {
		if gen.Resource == Diamond {
			return gen
		}
	}
	t.Fatal("arena has no diamond generator")
	return Generator{}
}

// TestHologramsSpawnOverTimedGenerators checks that OnInstanceStart puts a
// title + timer pair defaultHologramHeight above every diamond/emerald
// generator and nothing over the team iron forges.
func TestHologramsSpawnOverTimedGenerators(t *testing.T) {
	g, inst, _ := harness(t)
	timed := 0
	for _, gen := range g.arena.Generators {
		if gen.Resource.timed() {
			timed++
		}
	}
	if timed == 0 {
		t.Fatal("generated arena should have a diamond generator")
	}
	inst.mu.Lock()
	holos := append([]*fakeHologram(nil), inst.holos...)
	inst.mu.Unlock()
	if len(holos) != 2*timed {
		t.Fatalf("holograms: got %d, want %d (two per timed generator)", len(holos), 2*timed)
	}

	title, timer := diamondLines(t, g, holos)
	_, y, _ := diamondGenerator(t, g).dropPoint()
	if want := y + defaultHologramHeight - nameTagOffset + hologramLineGap; title.y != want || timer.y >= title.y {
		t.Errorf("hologram heights: title=%v (want %v) timer=%v over block y=%v", title.y, want, timer.y, y)
	}
}

// TestHologramHeightFromArena: an arena's HologramHeight (the map JSON's
// "hologramHeight") replaces the default.
func TestHologramHeightFromArena(t *testing.T) {
	g, _, _ := harness(t)
	g.arena.HologramHeight = 4
	inst := newFakeInstance()
	g.spawnHolograms(&game.Ctx{InstanceID: "test", Instance: inst})
	inst.mu.Lock()
	holos := append([]*fakeHologram(nil), inst.holos...)
	inst.mu.Unlock()
	title, _ := diamondLines(t, g, holos)
	_, y, _ := diamondGenerator(t, g).dropPoint()
	if want := y + 4 - nameTagOffset + hologramLineGap; title.y != want {
		t.Errorf("title at %v, want %v for a height of 4", title.y, want)
	}
}

// diamondLines finds the title and timer lines over the diamond generator.
func diamondLines(t *testing.T, g *bedWars, holos []*fakeHologram) (title, timer *fakeHologram) {
	t.Helper()
	x, _, z := diamondGenerator(t, g).dropPoint()
	for _, h := range holos {
		if h.x != x || h.z != z {
			continue
		}
		switch h.current() {
		case Diamond.title():
			title = h
		default:
			timer = h
		}
	}
	if title == nil || timer == nil {
		t.Fatal("diamond generator is missing its title or timer line")
	}
	return title, timer
}

// TestHologramTimerCountsDown checks the timer text follows the generator's
// interval grid and flips to "Full" once the forge pile is at its cap.
func TestHologramTimerCountsDown(t *testing.T) {
	g, inst, ctx := harness(t)
	dg := diamondGenerator(t, g)
	x, _, z := dg.dropPoint()
	var timer *fakeHologram
	inst.mu.Lock()
	for _, h := range inst.holos {
		if h.x == x && h.z == z && h.current() != Diamond.title() {
			timer = h
		}
	}
	inst.mu.Unlock()
	if timer == nil {
		t.Fatal("no diamond timer hologram")
	}

	// One second after a fire, interval-20 ticks remain.
	g.OnTick(ctx, dg.IntervalTicks+20)
	wantSecs := int((dg.IntervalTicks - 20 + 19) / 20)
	if got := timer.current(); !strings.Contains(got, "in §f"+strconv.Itoa(wantSecs)+"s") {
		t.Errorf("timer after 1s: %q, want %ds remaining", got, wantSecs)
	}
	// 5 ticks before the next fire → rounds up to 1s.
	g.OnTick(ctx, 2*dg.IntervalTicks-5)
	if got := timer.current(); !strings.Contains(got, "§f1s") {
		t.Errorf("timer 5 ticks before fire: %q, want 1s", got)
	}

	// Let the unattended forge fill up: the timer shows Full instead.
	for n := uint64(1); n <= uint64(dg.maxStack()); n++ {
		g.OnTick(ctx, n*dg.IntervalTicks)
	}
	if got := timer.current(); got != "§cFull" {
		t.Errorf("timer with a full pile: %q, want §cFull", got)
	}
}
