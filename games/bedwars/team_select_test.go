package bedwars

import (
	"fmt"
	"strings"
	"testing"

	"minecraft-server/game"
)

// useSelector right-clicks the team selector wool.
func useSelector(g *bedWars, ctx *game.Ctx, p *fakePlayer) {
	g.OnItemUse(ctx, p, game.ItemUse{Item: "minecraft:white_wool", Name: nameTeamSelector})
}

// pick opens the selector and clicks the team's wool.
func pick(t *testing.T, g *bedWars, ctx *game.Ctx, p *fakePlayer, team int) {
	t.Helper()
	useSelector(g, ctx, p)
	p.mu.Lock()
	click := p.menuClick
	p.mu.Unlock()
	if click == nil {
		t.Fatal("selector opened no menu")
	}
	click(game.MenuClick{Slot: teamMenuSlot(team, len(g.teams))})
}

func TestTeamMenuShowsTheTeams(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8)
	a := join(g, inst, ctx, "A", 1)
	useSelector(g, ctx, a)
	if a.menuTitle != teamMenuTitle {
		t.Fatalf("menu %q, want %q", a.menuTitle, teamMenuTitle)
	}
	want := []struct {
		slot int
		wool string
		name string
	}{
		{1, "minecraft:red_wool", "§cRed"},
		{3, "minecraft:blue_wool", "§9Blue"},
		{5, "minecraft:green_wool", "§aGreen"},
		{7, "minecraft:yellow_wool", "§eYellow"},
	}
	for _, w := range want {
		it := menuAt(a, w.slot)
		if it == nil || it.Item != w.wool || it.Name != w.name {
			t.Errorf("slot %d = %+v, want %s %q", w.slot, it, w.wool, w.name)
		}
	}
}

func TestPickedTeamIsHonouredAtStart(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8)
	a := join(g, inst, ctx, "A", 1)
	b := join(g, inst, ctx, "B", 2)
	pick(t, g, ctx, a, 2) // Green
	pick(t, g, ctx, b, 2) // Green too

	if it := menuAt(b, teamMenuSlot(2, 4)); it == nil || !it.Glint ||
		!strings.Contains(strings.Join(it.Lore, "|"), "2§7/§f4") {
		t.Fatalf("refreshed menu should mark Green picked by 2/4: %+v", it)
	}
	if err := g.ForceStart(ctx); err != nil {
		t.Fatal(err)
	}
	g.OnTick(ctx, 1)
	for _, p := range []*fakePlayer{a, b} {
		if tm, ok := g.teamOf(p); !ok || tm.Name != "Green" {
			t.Errorf("%s on %v, want Green", p.name, tm)
		}
	}
}

func TestFullTeamRefusesPicks(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8) // teams of 4
	var players []*fakePlayer
	for i := 0; i < 5; i++ {
		p := join(g, inst, ctx, fmt.Sprintf("P%d", i), int32(i+1))
		pick(t, g, ctx, p, 0) // everyone wants Red
		players = append(players, p)
	}
	last := players[4]
	if !strings.Contains(strings.Join(last.messages, "|"), "full") {
		t.Fatalf("5th pick of a 4-team not refused: %v", last.messages)
	}
	if err := g.ForceStart(ctx); err != nil {
		t.Fatal(err)
	}
	g.OnTick(ctx, 1)
	for _, p := range players[:4] {
		if tm, _ := g.teamOf(p); tm.Name != "Red" {
			t.Errorf("%s on %s, want Red", p.name, tm.Name)
		}
	}
	if tm, ok := g.teamOf(last); !ok || tm.Name == "Red" {
		t.Errorf("the overflow player landed on %v, want another team", tm)
	}
}

func TestLeavingDropsThePick(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8)
	a := join(g, inst, ctx, "A", 1)
	pick(t, g, ctx, a, 1)
	g.OnPlayerLeave(ctx, a)
	inst.remove(a)
	g.mu.Lock()
	_, kept := g.chosen[a.eid]
	g.mu.Unlock()
	if kept {
		t.Fatal("a player who left still holds a team slot")
	}
}

func TestSelectorDoesNothingOnceStarted(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8)
	a := join(g, inst, ctx, "A", 1)
	if err := g.ForceStart(ctx); err != nil {
		t.Fatal(err)
	}
	g.OnTick(ctx, 1)
	a.mu.Lock()
	a.menuTitle = ""
	a.mu.Unlock()
	useSelector(g, ctx, a)
	if a.menuTitle == teamMenuTitle {
		t.Fatal("team menu opened during the round")
	}
}
