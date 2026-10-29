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

	// Let the unattended forge fill up: the timer shows Full instead. (Tick
	// on until it is — the generator tiers speed it up along the way.)
	for n := uint64(2); n <= uint64(4*dg.maxStack()) && timer.current() != "§cFull"; n++ {
		g.OnTick(ctx, n*dg.IntervalTicks)
	}
	if got := timer.current(); got != "§cFull" {
		t.Errorf("timer with a full pile: %q, want §cFull", got)
	}
	if n := inst.DroppedItemsNear(float64(dg.Pos.X)+0.5, float64(dg.Pos.Y), float64(dg.Pos.Z)+0.5, 1.5, Diamond.item()); n != dg.maxStack() {
		t.Errorf("full pile holds %d diamonds, want the cap %d", n, dg.maxStack())
	}
}

// TestGeneratorHologramHeightOverrides: a generator's own height beats the
// arena's; generators without one keep the arena's.
func TestGeneratorHologramHeightOverrides(t *testing.T) {
	g, _, _ := harness(t)
	g.arena.HologramHeight = 3
	var own, other *Generator
	for i := range g.gens {
		if !g.gens[i].Resource.timed() {
			continue
		}
		if own == nil {
			own = &g.gens[i]
		} else if other == nil {
			other = &g.gens[i]
		}
	}
	if own == nil {
		t.Fatal("no timed generator")
	}
	own.HologramHeight = 5
	if got := g.hologramHeight(*own); got != 5 {
		t.Errorf("own height: %v, want 5", got)
	}
	if other != nil {
		if got := g.hologramHeight(*other); got != 3 {
			t.Errorf("arena height for a generator without one: %v, want 3", got)
		}
	}
	g.arena.HologramHeight = 0
	if got := g.hologramHeight(Generator{}); got != defaultHologramHeight {
		t.Errorf("fallback: %v, want %v", got, defaultHologramHeight)
	}

	// And the spawned title sits at the generator's own height.
	inst := newFakeInstance()
	g.spawnHolograms(&game.Ctx{InstanceID: "test", Instance: inst})
	x, y, z := own.dropPoint()
	want := y + 5 - nameTagOffset + hologramLineGap
	found := false
	for _, h := range inst.holos {
		if h.x == x && h.z == z && h.y == want {
			found = true
		}
	}
	if !found {
		t.Errorf("no title at y=%v over the overridden generator", want)
	}
}

func TestGeneratorHologramHeightFromConfig(t *testing.T) {
	cfg, err := parseArenaConfig([]byte(`{"teams":[{"name":"Red"},{"name":"Blue"}],
	  "generators":[
	    {"resource":"diamond","x":1,"y":64,"z":1,"hologramHeight":4.5},
	    {"resource":"emerald","x":2,"y":64,"z":2}
	  ]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Generators[0].HologramHeight; got != 4.5 {
		t.Fatalf("parsed height %v, want 4.5", got)
	}
	if got := cfg.Generators[1].HologramHeight; got != 0 {
		t.Fatalf("absent height parsed as %v", got)
	}
}
