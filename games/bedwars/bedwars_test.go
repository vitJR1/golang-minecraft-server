package bedwars

import (
	"math"
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
	given    map[string]int // namespaced item id → total count granted (= inventory)

	menuTitle string
	menuItems []game.MenuItem
	menuClick func(int)
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
func (p *fakePlayer) IsOp() bool  { return false }
func (p *fakePlayer) CountItem(item string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.given[item]
}
func (p *fakePlayer) TakeItem(item string, n int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.given[item] < n {
		return false
	}
	p.given[item] -= n
	return true
}
func (p *fakePlayer) OpenMenu(title string, _ int, items []game.MenuItem, onClick func(int)) {
	p.mu.Lock()
	p.menuTitle, p.menuItems, p.menuClick = title, items, onClick
	p.mu.Unlock()
}
func (p *fakePlayer) GiveItem(itemName string, count int) {
	p.mu.Lock()
	if p.given == nil {
		p.given = make(map[string]int)
	}
	p.given[itemName] += count
	p.mu.Unlock()
}

// fakeInstance is a minimal game.Instance recording broadcasts and block
// writes, with a mutable player list the logic can query.
type fakeInstance struct {
	mu         sync.Mutex
	players    map[int32]*fakePlayer
	blocks     map[world.Position]world.Block
	broadcasts []string
	ended      bool
	drops      []fakeDrop // every DropItem call, in order
	holos      []*fakeHologram
}

// fakeHologram records the floating text a game asked for.
type fakeHologram struct {
	mu      sync.Mutex
	x, y, z float64
	text    string
	removed bool
}

func (h *fakeHologram) SetText(text string) {
	h.mu.Lock()
	h.text = text
	h.mu.Unlock()
}
func (h *fakeHologram) Remove() {
	h.mu.Lock()
	h.removed = true
	h.mu.Unlock()
}
func (h *fakeHologram) current() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text
}

// fakeDrop records one DropItem call on the fake instance.
type fakeDrop struct {
	x, y, z float64
	item    string
	count   int
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
func (i *fakeInstance) SetBlocks(changes []world.BlockChange) {
	i.mu.Lock()
	for _, ch := range changes {
		i.blocks[ch.Pos] = ch.Block
	}
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

// SetPvP / SetInstantRespawn satisfy the game.Instance combat-toggle methods.
// The fake doesn't model combat, so they're no-ops.
func (i *fakeInstance) SetPvP(bool)            {}
func (i *fakeInstance) SetInstantRespawn(bool) {}

func (i *fakeInstance) DropItem(x, y, z float64, item string, count int) bool {
	i.mu.Lock()
	i.drops = append(i.drops, fakeDrop{x, y, z, item, count})
	i.mu.Unlock()
	return true
}

func (i *fakeInstance) SpawnHologram(x, y, z float64, text string) game.Hologram {
	h := &fakeHologram{x: x, y: y, z: z, text: text}
	i.mu.Lock()
	i.holos = append(i.holos, h)
	i.mu.Unlock()
	return h
}

// dropsAt returns the recorded drops made exactly at (x, y, z).
func (i *fakeInstance) dropsAt(x, y, z float64) []fakeDrop {
	i.mu.Lock()
	defer i.mu.Unlock()
	var out []fakeDrop
	for _, d := range i.drops {
		if d.x == x && d.y == y && d.z == z {
			out = append(out, d)
		}
	}
	return out
}

// DroppedItemsNear sums the recorded drops of item within radius — the fake
// never "collects", so this models an unattended forge.
func (i *fakeInstance) DroppedItemsNear(x, y, z, radius float64, item string) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	n := 0
	for _, d := range i.drops {
		if d.item == item && math.Abs(d.x-x) <= radius && math.Abs(d.y-y) <= radius && math.Abs(d.z-z) <= radius {
			n += d.count
		}
	}
	return n
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
			if got := w.GetBlock(bp); got.Name != teams[i].Bed.Name {
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

// redIronGenerator returns red's own team iron generator.
func redIronGenerator(t *testing.T, g *bedWars, red *fakePlayer) Generator {
	t.Helper()
	redTeam := g.byEntity[red.EntityID()]
	for _, gen := range g.arena.Generators {
		if gen.Resource == Iron && gen.TeamID == redTeam {
			return gen
		}
	}
	t.Fatal("no iron generator for red's team")
	return Generator{}
}

// TestGeneratorDropsAtForge checks that the default dropGranter spawns the
// iron as a dropped item at the generator block — not straight into the
// player's inventory — when the generator fires on its interval.
func TestGeneratorDropsAtForge(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	gen := redIronGenerator(t, g, red)

	g.OnTick(ctx, gen.IntervalTicks) // one full interval → iron generator fires once

	red.mu.Lock()
	given := red.given["minecraft:iron_ingot"]
	red.mu.Unlock()
	if given != 0 {
		t.Errorf("iron went straight to the inventory: %d", given)
	}
	// Every team's iron forge fires on the same interval; look at red's only.
	drops := inst.dropsAt(gen.dropPoint())
	if len(drops) != 1 {
		t.Fatalf("drops at red's forge after one interval: got %d, want 1", len(drops))
	}
	if d := drops[0]; d.item != "minecraft:iron_ingot" || d.count != 1 {
		t.Errorf("drop = %+v, want 1 iron_ingot", d)
	}
}

// TestGeneratorPileCap checks that an unattended forge stops producing once
// MaxStack units lie there, and resumes with the default cap semantics.
func TestGeneratorPileCap(t *testing.T) {
	g, inst, ctx := harness(t)
	red := join(g, inst, ctx, "red", 1)
	gen := redIronGenerator(t, g, red)
	cap := gen.maxStack()
	if cap != defaultGenMaxStack(Iron) {
		t.Fatalf("unset MaxStack should use the iron default, got %d", cap)
	}

	for n := uint64(1); n <= uint64(cap)+10; n++ {
		g.OnTick(ctx, n*gen.IntervalTicks)
	}
	got := len(inst.dropsAt(gen.dropPoint()))
	if got != cap {
		t.Errorf("drops with nobody collecting: got %d, want cap %d", got, cap)
	}
}

// TestInventoryGranterStillGives keeps the opt-in auto-collect economy
// working for arenas that swap it in via WithGranter.
func TestInventoryGranterStillGives(t *testing.T) {
	g, inst, ctx := harness(t)
	g.WithGranter(inventoryGranter{})
	red := join(g, inst, ctx, "red", 1)
	gen := redIronGenerator(t, g, red)

	g.OnTick(ctx, gen.IntervalTicks)

	red.mu.Lock()
	got := red.given["minecraft:iron_ingot"]
	red.mu.Unlock()
	if got != 1 {
		t.Errorf("iron granted after one interval: got %d, want 1", got)
	}
}

func TestDuelModeRegistered(t *testing.T) {
	def, ok := game.GetDef(KindDuel)
	if !ok {
		t.Fatal("bedwars-1x1 not registered")
	}
	if def.MinPlayers != 2 || def.MaxPlayers != 2 {
		t.Errorf("player bounds: got %d..%d, want 2..2", def.MinPlayers, def.MaxPlayers)
	}
	g := def.New().(*bedWars)
	if len(g.teams) != 2 || g.teamSize != 1 {
		t.Errorf("teams=%d teamSize=%d, want 2 teams of 1", len(g.teams), g.teamSize)
	}
	if len(g.arena.BedBlocks) != 2 {
		t.Errorf("arena beds: %d, want 2", len(g.arena.BedBlocks))
	}
}

// TestDuelOneKillAfterBedBreakWins runs a full 1×1: two joiners land on
// different teams, breaking the enemy bed then killing them ends the round.
func TestDuelOneKillAfterBedBreakWins(t *testing.T) {
	teams := buildTeams(2)
	g := newBedWars(buildArena(teams), teams, 1)
	inst := newFakeInstance()
	ctx := &game.Ctx{InstanceID: "duel", Instance: inst}
	g.OnInstanceStart(ctx)
	red := join(g, inst, ctx, "red", 1)
	blue := join(g, inst, ctx, "blue", 2)

	g.mu.Lock()
	if g.byEntity[red.eid] == g.byEntity[blue.eid] {
		t.Fatal("duellists must be on different teams")
	}
	blueTeam := g.byEntity[blue.eid]
	g.mu.Unlock()

	if !g.OnBlockBreak(ctx, red, g.arena.BedBlocks[blueTeam][0]) {
		t.Fatal("red should be able to break blue's bed")
	}
	g.OnPlayerAttack(ctx, red, blue)
	if blue.Pose().Gamemode != player.Spectator {
		t.Error("blue should be eliminated once bedless")
	}
	if !inst.sawBroadcast("Red team wins") {
		t.Errorf("expected Red win broadcast, got %v", inst.broadcasts)
	}
}
