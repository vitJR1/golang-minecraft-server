package bedwars

import (
	"strings"
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
)

// lobbyHarness is harness with a two-player lobby.
func lobbyHarness(t *testing.T, minPlayers int) (*bedWars, *fakeInstance, *game.Ctx) {
	t.Helper()
	teams := buildTeams(4)
	arena := buildArena(teams)
	g := newBedWars(arena, teams, 4).WithMinPlayers(minPlayers)
	inst := newFakeInstance()
	ctx := &game.Ctx{InstanceID: "test", Instance: inst}
	g.OnInstanceStart(ctx)
	return g, inst, ctx
}

func TestLobbyWaitsThenStarts(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 2)
	a := join(g, inst, ctx, "A", 1)
	// Parked at the centre, no team, no kit, adventure.
	if pose := a.Pose(); pose.X != 0.5 || pose.Z != 0.5 || pose.Y != baseY+1 || pose.Gamemode != player.Adventure {
		t.Errorf("waiting player pose: %+v", pose)
	}
	if _, ok := g.teamOf(a); ok {
		t.Error("waiting player must not have a team")
	}
	// No kit — only the team selector wool, and flight while waiting.
	if st := a.slot(36); st.Item != "minecraft:white_wool" || st.Name != nameTeamSelector || !st.NoDrop {
		t.Errorf("waiting player's first slot = %+v, want the team selector", st)
	}
	if !a.canFly() {
		t.Error("waiting players should fly")
	}
	if !strings.Contains(strings.Join(a.sidebar, "\n"), "Players: §a1§f/§a2") {
		t.Errorf("lobby sidebar: %v", a.sidebar)
	}
	g.OnTick(ctx, 100)
	if g.started {
		t.Fatal("one player must not start the round")
	}

	b := join(g, inst, ctx, "B", 2)
	inst.mu.Lock()
	chat := strings.Join(inst.broadcasts, "\n")
	inst.mu.Unlock()
	if !strings.Contains(chat, "starts in §e10") {
		t.Errorf("countdown not announced:\n%s", chat)
	}
	// Countdown runs from the last tick seen (100) for lobbyCountdownTicks.
	for tick := uint64(101); tick < 100+lobbyCountdownTicks; tick++ {
		g.OnTick(ctx, tick)
	}
	if g.started {
		t.Fatal("started before the countdown ended")
	}
	if !strings.Contains(strings.Join(a.titles, "|"), "§e1") {
		t.Errorf("countdown titles missing: %v", a.titles)
	}
	g.OnTick(ctx, 100+lobbyCountdownTicks)
	if !g.started {
		t.Fatal("round should have started")
	}
	ta, okA := g.teamOf(a)
	tb, okB := g.teamOf(b)
	if !okA || !okB || ta.ID == tb.ID {
		t.Errorf("players must land on different teams: %v %v", ta, tb)
	}
	if a.canFly() {
		t.Error("flight must end when the round starts")
	}
	if a.Pose().Gamemode != player.Survival || a.slot(36).Item != "minecraft:wooden_sword" {
		t.Errorf("started player should be in survival with a kit: %+v slot0=%+v", a.Pose(), a.slot(36))
	}
	if sp := g.arena.Spawns[ta.ID].Position; a.Pose().X != float64(sp.X)+0.5 {
		t.Errorf("A at x=%v, want island spawn %v", a.Pose().X, sp)
	}
	g.mu.Lock()
	engaged, startTick := g.engaged, g.startTick
	g.mu.Unlock()
	if !engaged || startTick != 100+lobbyCountdownTicks {
		t.Errorf("engaged=%v startTick=%d", engaged, startTick)
	}
	// Late joiner goes straight onto a team.
	c := join(g, inst, ctx, "C", 3)
	if _, ok := g.teamOf(c); !ok || c.Pose().Gamemode != player.Survival {
		t.Error("joining a running round must assign a team at once")
	}
}

func TestLobbyCountdownCancelsWhenSomeoneLeaves(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 2)
	a := join(g, inst, ctx, "A", 1)
	join(g, inst, ctx, "B", 2)
	g.OnTick(ctx, 1)
	g.OnPlayerLeave(ctx, a)
	inst.remove(a)
	for tick := uint64(2); tick <= lobbyCountdownTicks+5; tick++ {
		g.OnTick(ctx, tick)
	}
	if g.started {
		t.Fatal("countdown must be cancelled when the room drops below the minimum")
	}
	inst.mu.Lock()
	chat := strings.Join(inst.broadcasts, "\n")
	inst.mu.Unlock()
	if !strings.Contains(chat, "cancelled") {
		t.Errorf("cancellation not announced:\n%s", chat)
	}
}

func TestLobbyProtectsMapAndRescuesFallers(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8)
	a := join(g, inst, ctx, "A", 1)
	if g.OnBlockBreak(ctx, a, g.arena.BedBlocks[1][0]) {
		t.Error("beds must be safe while waiting")
	}
	if g.OnBlockPlace(ctx, a, g.arena.Spawns[0].Position, g.teams[0].team.Wool) {
		t.Error("no building while waiting")
	}
	a.Teleport(0.5, voidY-10, 0.5)
	g.OnTick(ctx, voidScanInterval)
	if pose := a.Pose(); pose.Dead || pose.Y != baseY+1 {
		t.Errorf("faller should be put back on the centre, got %+v", pose)
	}
}

func TestGeneratorTimelineCountsFromRoundStart(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 1)
	a := join(g, inst, ctx, "A", 1)
	for tick := uint64(1); tick <= lobbyCountdownTicks; tick++ {
		g.OnTick(ctx, tick)
	}
	if !g.started {
		t.Fatal("should have started")
	}
	// Six minutes after the *start*, not after instance creation.
	g.OnTick(ctx, lobbyCountdownTicks+6*60*20-20)
	if strings.Contains(strings.Join(a.sidebar, "\n"), "Diamond: §fTier II") {
		t.Error("tier II too early")
	}
	g.OnTick(ctx, lobbyCountdownTicks+6*60*20)
	if !strings.Contains(strings.Join(a.sidebar, "\n"), "Diamond: §fTier II") {
		t.Errorf("tier II expected at start+6:00: %v", a.sidebar)
	}
}

func TestDefaultLobbyIsTheCentreDiamond(t *testing.T) {
	a := buildArena(buildTeams(4))
	if a.Lobby.Position != diamondGenerator(t, &bedWars{arena: a}).Pos {
		t.Errorf("lobby %v, want the diamond generator", a.Lobby)
	}
}

// TestForceStartSkipsTheWait: /forcestart starts an 8-player round with
// two players on the very next tick, through the normal start path.
func TestForceStartSkipsTheWait(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8)
	if err := g.ForceStart(ctx); err == nil {
		t.Fatal("force-starting an empty lobby should fail")
	}
	a := join(g, inst, ctx, "A", 1)
	b := join(g, inst, ctx, "B", 2)
	g.OnTick(ctx, 50)
	if g.started {
		t.Fatal("2 of 8 must not start on their own")
	}
	if err := g.ForceStart(ctx); err != nil {
		t.Fatal(err)
	}
	g.OnTick(ctx, 51)
	if !g.started {
		t.Fatal("force-started round didn't start on the next tick")
	}
	ta, okA := g.teamOf(a)
	tb, okB := g.teamOf(b)
	if !okA || !okB || ta.ID == tb.ID || a.Pose().Gamemode != player.Survival {
		t.Fatalf("players not dealt onto teams: %v %v", ta, tb)
	}
	if err := g.ForceStart(ctx); err == nil {
		t.Fatal("force-starting a running round should fail")
	}
}

// TestForceStartSurvivesALeave: a forced start isn't cancelled by someone
// leaving before it runs (the room is below minPlayers anyway).
func TestForceStartSurvivesALeave(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8)
	a := join(g, inst, ctx, "A", 1)
	join(g, inst, ctx, "B", 2)
	g.OnTick(ctx, 10)
	if err := g.ForceStart(ctx); err != nil {
		t.Fatal(err)
	}
	g.OnPlayerLeave(ctx, a)
	inst.remove(a)
	g.OnTick(ctx, 11)
	if !g.started {
		t.Fatal("a leave cancelled the forced start")
	}
}
