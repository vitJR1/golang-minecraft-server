package bedwars

import (
	"strings"
	"sync"
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// --- test doubles ----------------------------------------------------------

// fakePlayer is a minimal game.PlayerHandle for driving the logic.
type fakePlayer struct {
	name string
	eid  int32

	mu       sync.Mutex
	x, y, z  float64
	gamemode player.Gamemode
	messages []string
}

func newFakePlayer(name string, eid int32) *fakePlayer {
	return &fakePlayer{name: name, eid: eid, y: baseY + 1, gamemode: player.Adventure}
}

func (p *fakePlayer) Name() string    { return p.name }
func (p *fakePlayer) EntityID() int32 { return p.eid }
func (p *fakePlayer) Pose() player.Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return player.Snapshot{X: p.x, Y: p.y, Z: p.z, Gamemode: p.gamemode}
}
func (p *fakePlayer) Teleport(x, y, z float64) {
	p.mu.Lock()
	p.x, p.y, p.z = x, y, z
	p.mu.Unlock()
}
func (p *fakePlayer) SendMessage(text string) {
	p.mu.Lock()
	p.messages = append(p.messages, text)
	p.mu.Unlock()
}
func (p *fakePlayer) SetGamemode(g player.Gamemode) {
	p.mu.Lock()
	p.gamemode = g
	p.mu.Unlock()
}
func (p *fakePlayer) Kick(string) {}

// fakeInstance is a minimal game.Instance recording broadcasts and block
// writes, with a mutable player list the logic can query.
type fakeInstance struct {
	mu         sync.Mutex
	players    map[int32]*fakePlayer
	blocks     map[world.Position]world.Block
	broadcasts []string
	ended      bool
}

func newFakeInstance() *fakeInstance {
	return &fakeInstance{
		players: make(map[int32]*fakePlayer),
		blocks:  make(map[world.Position]world.Block),
	}
}

func (i *fakeInstance) add(p *fakePlayer) {
	i.mu.Lock()
	i.players[p.eid] = p
	i.mu.Unlock()
}

func (i *fakeInstance) ID() string { return "test" }
func (i *fakeInstance) SetBlock(p world.Position, b world.Block) {
	i.mu.Lock()
	i.blocks[p] = b
	i.mu.Unlock()
}
func (i *fakeInstance) GetBlock(p world.Position) world.Block {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.blocks[p]
}
func (i *fakeInstance) BroadcastChat(_, msg string) {
	i.mu.Lock()
	i.broadcasts = append(i.broadcasts, msg)
	i.mu.Unlock()
}
func (i *fakeInstance) PlayerCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.players)
}
func (i *fakeInstance) Players() []game.PlayerHandle {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]game.PlayerHandle, 0, len(i.players))
	for _, p := range i.players {
		out = append(out, p)
	}
	return out
}
func (i *fakeInstance) PlayerByName(name string) (game.PlayerHandle, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, p := range i.players {
		if p.name == name {
			return p, true
		}
	}
	return nil, false
}
func (i *fakeInstance) EndGame() {
	i.mu.Lock()
	i.ended = true
	i.mu.Unlock()
}

func (i *fakeInstance) sawBroadcast(substr string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, m := range i.broadcasts {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

// harness wires a fresh logic + instance + ctx for a test.
func harness(t *testing.T) (*bedWars, *fakeInstance, *game.Ctx) {
	t.Helper()
	teams := buildTeams(4)
	arena := buildArena(teams)
	g := newBedWars(arena, teams, 4)
	inst := newFakeInstance()
	ctx := &game.Ctx{InstanceID: "test", Instance: inst}
	g.OnInstanceStart(ctx)
	return g, inst, ctx
}

// join adds a player both to the instance list and via the join hook.
func join(g *bedWars, inst *fakeInstance, ctx *game.Ctx, name string, eid int32) *fakePlayer {
	p := newFakePlayer(name, eid)
	inst.add(p)
	g.OnPlayerJoin(ctx, p)
	return p
}

// --- tests -----------------------------------------------------------------

func TestRegistered(t *testing.T) {
	def, ok := game.GetDef("bedwars")
	if !ok {
		t.Fatal("bedwars not registered")
	}
	if def.MinPlayers != 2 || def.MaxPlayers != 16 {
		t.Errorf("player bounds: got %d..%d, want 2..16", def.MinPlayers, def.MaxPlayers)
	}
	if def.Template == nil || def.Template.BlockCount() == 0 {
		t.Fatal("template is empty")
	}
}

func TestArenaHasFourBeds(t *testing.T) {
	a := buildArena(buildTeams(4))
	if len(a.BedBlocks) != 4 {
		t.Fatalf("BedBlocks: got %d teams, want 4", len(a.BedBlocks))
	}
	for id, beds := range a.BedBlocks {
		if len(beds) != 2 {
			t.Errorf("team %d: got %d bed blocks, want 2", id, len(beds))
		}
		for _, bp := range beds {
			owner, ok := a.bedTeam(bp)
			if !ok || owner != id {
				t.Errorf("bedTeam(%v): got (%d,%v), want (%d,true)", bp, owner, ok, id)
			}
		}
	}
}

func TestBalancedTeamAssignment(t *testing.T) {
	g, inst, ctx := harness(t)
	for i := int32(0); i < 4; i++ {
		join(g, inst, ctx, "p", i)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, ts := range g.teams {
		if len(ts.members) != 1 {
			t.Errorf("team %d: got %d members, want 1", id, len(ts.members))
		}
	}
}

func TestCannotBreakOwnBed(t *testing.T) {
	g, inst, ctx := harness(t)
	p := join(g, inst, ctx, "red", 1) // first join → team 0 (Red)
	ownBed := g.arena.BedBlocks[0][0]
	if g.OnBlockBreak(ctx, p, ownBed) {
		t.Error("breaking own bed should be vetoed")
	}
	if !g.teams[0].bedAlive {
		t.Error("own bed should still be alive after vetoed break")
	}
}

func TestBreakingEnemyBedKillsIt(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1) // team 0
	_ = join(g, inst, ctx, "blue", 2)   // team 1
	enemyBed := g.arena.BedBlocks[1][0] // Blue's bed
	if !g.OnBlockBreak(ctx, red, enemyBed) {
		t.Fatal("breaking an enemy bed should be allowed")
	}
	if g.teams[1].bedAlive {
		t.Error("blue bed should be dead after break")
	}
	// Both halves cleared to air.
	for _, bp := range g.arena.BedBlocks[1] {
		if inst.GetBlock(bp) != world.Air {
			t.Errorf("bed half %v not cleared", bp)
		}
	}
	if !inst.sawBroadcast("bed was destroyed") {
		t.Error("expected bed-destroyed broadcast")
	}
}

func TestKillRespawnsWhenBedAlive(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	blue := join(g, inst, ctx, "blue", 2)
	// Move blue away, then red kills blue → respawn at blue's island.
	blue.Teleport(999, 999, 999)
	g.OnPlayerAttack(ctx, red, blue)
	if blue.Pose().Gamemode == player.Spectator {
		t.Error("victim with a live bed should respawn, not spectate")
	}
	spawn := g.arena.Spawns[1].Position
	if int(blue.Pose().X) != spawn.X || int(blue.Pose().Z) != spawn.Z {
		t.Errorf("respawn pos: got (%v,%v), want (%d,%d)",
			blue.Pose().X, blue.Pose().Z, spawn.X, spawn.Z)
	}
}

func TestKillEliminatesWhenBedDead_AndLastTeamWins(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	blue := join(g, inst, ctx, "blue", 2)

	// Red breaks Blue's bed, then kills Blue → elimination → Red wins.
	g.OnBlockBreak(ctx, red, g.arena.BedBlocks[1][0])
	g.OnPlayerAttack(ctx, red, blue)

	if blue.Pose().Gamemode != player.Spectator {
		t.Error("victim with a dead bed should be eliminated to Spectator")
	}
	g.mu.Lock()
	stillIn := g.teams[1].inPlay()
	over := g.over
	g.mu.Unlock()
	if stillIn {
		t.Error("blue team should be out after its last member is eliminated")
	}
	if !over {
		t.Error("round should be over with one team left")
	}
	if !inst.sawBroadcast("Red team wins") {
		t.Error("expected Red win broadcast")
	}
}

func TestVoidKill(t *testing.T) {
	g, inst, ctx := harness(t)
	_ = join(g, inst, ctx, "red", 1)
	blue := join(g, inst, ctx, "blue", 2)
	blue.Teleport(0, voidY-5, 0) // fall below the void line
	g.checkVoid(ctx)
	// Bed alive → respawn (not spectator), and a void message broadcast.
	if blue.Pose().Gamemode == player.Spectator {
		t.Error("void death with live bed should respawn")
	}
	if !inst.sawBroadcast("fell into the void") {
		t.Error("expected void-death broadcast")
	}
}

func TestModeArenaScalesWithTeamCount(t *testing.T) {
	for _, n := range []int{2, 3, 4, 6, 8} {
		a := buildArena(buildTeams(n))
		if len(a.Spawns) != n || len(a.BedBlocks) != n {
			t.Errorf("n=%d: spawns=%d beds=%d, want %d each", n, len(a.Spawns), len(a.BedBlocks), n)
		}
		// Islands must not collide: every team's bed centre is distinct.
		seen := map[world.Position]bool{}
		for _, beds := range a.BedBlocks {
			head := beds[0]
			if seen[head] {
				t.Errorf("n=%d: duplicate island centre %v", n, head)
			}
			seen[head] = true
		}
	}
}

func TestTeamCapacityRespected(t *testing.T) {
	// 2 teams of 3: filling 6 players must give exactly 3 per team, never 4.
	teams := buildTeams(2)
	g := newBedWars(buildArena(teams), teams, 3)
	inst := newFakeInstance()
	ctx := &game.Ctx{InstanceID: "test", Instance: inst}
	g.OnInstanceStart(ctx)
	for i := int32(0); i < 6; i++ {
		join(g, inst, ctx, "p", i)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, ts := range g.teams {
		if len(ts.members) != 3 {
			t.Errorf("team %d: got %d members, want 3 (cap)", id, len(ts.members))
		}
	}
}

func TestSchemArenaFromRealMap(t *testing.T) {
	const path = "../../schem/templates/bedwars/badwars_dota_map.schem"
	teams := buildTeams(4)
	a, err := buildSchemArena(path, teams)
	if err != nil {
		t.Fatalf("buildSchemArena: %v", err)
	}
	if len(a.BedBlocks) != 4 {
		t.Fatalf("beds: got %d teams, want 4", len(a.BedBlocks))
	}
	w := a.Template.Instantiate()
	for i := range teams {
		if len(a.BedBlocks[i]) != 2 {
			t.Errorf("team %d: got %d bed blocks, want 2", i, len(a.BedBlocks[i]))
		}
		// Beds must be recoloured to the team's colour in the world.
		for _, bp := range a.BedBlocks[i] {
			if got := w.GetBlock(bp); got != teams[i].Bed {
				t.Errorf("team %d bed at %v: got %s, want %s", i, bp, got.Name, teams[i].Bed.Name)
			}
			if owner, ok := a.bedTeam(bp); !ok || owner != i {
				t.Errorf("bedTeam(%v): got (%d,%v), want (%d,true)", bp, owner, ok, i)
			}
		}
		// Each team gets exactly one iron forge.
	}
	// Distinct bed colours across the four teams.
	seen := map[int32]bool{}
	for i := range teams {
		id := teams[i].Bed.StateID
		if seen[id] {
			t.Errorf("duplicate bed colour StateID %d", id)
		}
		seen[id] = true
	}
	// One iron generator per team + at least the central diamond.
	iron, neutralGen := 0, 0
	for _, g := range a.Generators {
		switch {
		case g.TeamID == neutral:
			neutralGen++
		case g.Resource == Iron:
			iron++
		}
	}
	if iron != 4 {
		t.Errorf("iron generators: got %d, want 4", iron)
	}
	if neutralGen < 1 {
		t.Errorf("neutral generators: got %d, want ≥1", neutralGen)
	}
}

func TestMapProtectionAndPlacedBlocks(t *testing.T) {
	g, inst, ctx := harness(t)
	p := join(g, inst, ctx, "red", 1)
	mapBlock := world.Position{X: 0, Y: baseY, Z: 0} // central island, not placed
	if g.OnBlockBreak(ctx, p, mapBlock) {
		t.Error("breaking an original map block should be vetoed")
	}
	placed := world.Position{X: 5, Y: baseY + 1, Z: 5}
	g.OnBlockPlace(ctx, p, placed, world.Stone)
	if !g.OnBlockBreak(ctx, p, placed) {
		t.Error("a player-placed block should be breakable")
	}
}
