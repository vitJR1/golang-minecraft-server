package bedwars

import (
	"encoding/json"
	"os"
	"testing"

	"minecraft-server/game"
	"minecraft-server/schem"
	"minecraft-server/world"
)

const sampleArenaJSON = `{
  "teams": [
    {"name":"Red","spawn":{"x":1,"y":65,"z":1,"yaw":90},"beds":[{"x":1,"y":65,"z":3}],
     "villagers":[{"type":"item","x":2,"y":65,"z":2,"yaw":0}]},
    {"name":"Blue","spawn":{"x":-1,"y":65,"z":-1,"yaw":270},"beds":[{"x":-1,"y":65,"z":-3}]}
  ],
  "generators": [
    {"resource":"iron","x":1,"y":65,"z":0,"intervalTicks":40,"team":0},
    {"resource":"diamond","x":0,"y":65,"z":0}
  ]
}`

func TestBuildConfigArena(t *testing.T) {
	var cfg arenaConfig
	if err := json.Unmarshal([]byte(sampleArenaJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	teams := buildTeams(2)
	a, err := buildConfigArena(world.NewTemplate(), cfg, teams)
	if err != nil {
		t.Fatal(err)
	}

	// Spawns from config.
	if a.Spawns[0].Position != (world.Position{X: 1, Y: 65, Z: 1}) || a.Spawns[0].Yaw != 90 {
		t.Errorf("team0 spawn: %+v", a.Spawns[0])
	}
	// Beds recoloured to team beds + ownership recorded.
	bed := world.Position{X: 1, Y: 65, Z: 3}
	if a.bedOwner[bed] != 0 {
		t.Errorf("bed owner: %v", a.bedOwner)
	}
	if got := a.Template.Instantiate().GetBlock(bed); got != world.RedBed {
		t.Errorf("bed block: got %+v, want RedBed", got)
	}
	// Generators: iron(team0, interval 40) + diamond(neutral, default 600).
	if len(a.Generators) != 2 {
		t.Fatalf("generators: %d", len(a.Generators))
	}
	if a.Generators[0].Resource != Iron || a.Generators[0].TeamID != 0 || a.Generators[0].IntervalTicks != 40 {
		t.Errorf("iron gen: %+v", a.Generators[0])
	}
	if a.Generators[1].Resource != Diamond || a.Generators[1].TeamID != neutral || a.Generators[1].IntervalTicks != 600 {
		t.Errorf("diamond gen: %+v", a.Generators[1])
	}
	// Villager spawned as a world entity.
	ents := a.Template.Instantiate().Entities()
	var villagers int
	for _, e := range ents {
		if e.Type == "minecraft:villager" {
			villagers++
		}
	}
	if villagers != 1 {
		t.Errorf("villagers: got %d, want 1", villagers)
	}
}

func TestArenaBuilderRegistered(t *testing.T) {
	b, ok := game.GetArenaBuilder("bedwars")
	if !ok {
		t.Fatal("bedwars arena builder not registered")
	}
	def, err := b("bw-test", "BW Test", world.NewTemplate(), []byte(sampleArenaJSON))
	if err != nil {
		t.Fatal(err)
	}
	if def.ID != "bw-test" || def.MaxPlayers != 2*4 || def.New == nil {
		t.Errorf("def: %+v", def)
	}
}

func TestBuildArenaConfigErrors(t *testing.T) {
	cases := []string{
		``,             // empty
		`{"teams":[]}`, // too few teams
		`{not json`,    // malformed
	}
	for _, j := range cases {
		if _, err := buildBedwarsArenaDef("x", "x", world.NewTemplate(), []byte(j)); err == nil {
			t.Errorf("expected error for config %q", j)
		}
	}
}

// TestRealMapArenaFromConfig builds the dota map arena from its committed JSON
// config end-to-end, validating coordinates line up with the real map.
func TestRealMapArenaFromConfig(t *testing.T) {
	s, err := schem.LoadFile("../../" + defaultMapPath)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := s.ToTemplateAt(-int(s.Width)/2, baseY, -int(s.Length)/2)
	cfg, err := os.ReadFile("../../schem/templates/bedwars/badwars_dota_map.json")
	if err != nil {
		t.Fatal(err)
	}
	def, err := buildBedwarsArenaDef("bw-real", "Dota", tmpl, cfg)
	if err != nil {
		t.Fatalf("build from real config: %v", err)
	}
	w := def.Template.Instantiate()

	// Villagers from config are present.
	var villagers int
	for _, e := range w.Entities() {
		if e.Type == "minecraft:villager" {
			villagers++
		}
	}
	if villagers < 2 {
		t.Errorf("villagers from config: got %d, want ≥2", villagers)
	}
	// The map's original beds are still present (block entities) so they render.
	bep, _ := any(w).(world.BlockEntityProvider)
	beds := 0
	for _, typ := range bep.BlockEntities() {
		if typ == "minecraft:bed" {
			beds++
		}
	}
	if beds == 0 {
		t.Error("expected bed block entities in built arena")
	}
}

// fourTeamArenaJSON is a ring-ordered 4-base config (Red -Z, Blue +X,
// Green +Z, Yellow -X) with a per-team iron generator each plus a neutral
// diamond — the shape of the shipped DOTA config.
const fourTeamArenaJSON = `{
  "teams": [
    {"name":"Red","spawn":{"x":0,"y":65,"z":-10,"yaw":0},"beds":[{"x":0,"y":65,"z":-8}],
     "villagers":[{"type":"item","x":1,"y":65,"z":-10,"yaw":0}]},
    {"name":"Blue","spawn":{"x":10,"y":65,"z":0,"yaw":90},"beds":[{"x":8,"y":65,"z":0}],
     "villagers":[{"type":"item","x":10,"y":65,"z":1,"yaw":90}]},
    {"name":"Green","spawn":{"x":0,"y":65,"z":10,"yaw":180},"beds":[{"x":0,"y":65,"z":8}],
     "villagers":[{"type":"item","x":-1,"y":65,"z":10,"yaw":180}]},
    {"name":"Yellow","spawn":{"x":-10,"y":65,"z":0,"yaw":270},"beds":[{"x":-8,"y":65,"z":0}],
     "villagers":[{"type":"item","x":-10,"y":65,"z":-1,"yaw":270}]}
  ],
  "generators": [
    {"resource":"iron","x":0,"y":65,"z":-10,"team":0},
    {"resource":"iron","x":10,"y":65,"z":0,"team":1},
    {"resource":"iron","x":0,"y":65,"z":10,"team":2},
    {"resource":"iron","x":-10,"y":65,"z":0,"team":3},
    {"resource":"diamond","x":0,"y":65,"z":0}
  ]
}`

func TestDuelArenaBuilder(t *testing.T) {
	b, ok := game.GetArenaBuilder(KindDuel)
	if !ok {
		t.Fatal("bedwars-1x1 arena builder not registered")
	}
	def, err := b("duel-test", "Duel", world.NewTemplate(), []byte(fourTeamArenaJSON))
	if err != nil {
		t.Fatal(err)
	}
	// Two teams of one → 2..2 players.
	if def.MinPlayers != 2 || def.MaxPlayers != 2 {
		t.Errorf("player bounds: got %d..%d, want 2..2", def.MinPlayers, def.MaxPlayers)
	}

	g := def.New().(*bedWars)
	if len(g.teams) != 2 || g.teamSize != 1 {
		t.Fatalf("teams=%d teamSize=%d, want 2 teams of 1", len(g.teams), g.teamSize)
	}
	// Opposite bases were picked (config 0 and 2: Red -Z and Green +Z).
	if g.arena.Spawns[0].Position != (world.Position{X: 0, Y: 65, Z: -10}) ||
		g.arena.Spawns[1].Position != (world.Position{X: 0, Y: 65, Z: 10}) {
		t.Errorf("spawns: %+v", g.arena.Spawns)
	}
	w := def.Template.Instantiate()
	// Kept beds are recoloured to the duel teams (Red, Blue); dropped bases'
	// beds are left untouched (not owned) so the map protection covers them.
	if got := w.GetBlock(world.Position{X: 0, Y: 65, Z: -8}); got != world.RedBed {
		t.Errorf("team0 bed: %+v, want RedBed", got)
	}
	if got := w.GetBlock(world.Position{X: 0, Y: 65, Z: 8}); got != world.BlueBed {
		t.Errorf("team1 bed: %+v, want BlueBed", got)
	}
	for _, p := range []world.Position{{X: 8, Y: 65, Z: 0}, {X: -8, Y: 65, Z: 0}} {
		if _, owned := g.arena.bedTeam(p); owned {
			t.Errorf("dropped base bed %v must not be owned", p)
		}
		if got := w.GetBlock(p); got != world.Air {
			t.Errorf("dropped base bed %v must be left as map block, got %+v", p, got)
		}
	}
	// Generators: the two kept irons, remapped to 0/1, plus the neutral diamond.
	if len(g.arena.Generators) != 3 {
		t.Fatalf("generators: %d, want 3: %+v", len(g.arena.Generators), g.arena.Generators)
	}
	wantTeams := []int{0, 1, neutral}
	for i, gen := range g.arena.Generators {
		if gen.TeamID != wantTeams[i] {
			t.Errorf("generator %d team: got %d, want %d", i, gen.TeamID, wantTeams[i])
		}
	}
	// Only the kept bases' villagers are spawned.
	var villagers int
	for _, e := range w.Entities() {
		if e.Type == "minecraft:villager" {
			villagers++
		}
	}
	if villagers != 2 {
		t.Errorf("villagers: got %d, want 2", villagers)
	}
}

func TestDuelArenaTwoTeamConfigUsedAsIs(t *testing.T) {
	cfg, err := parseArenaConfig([]byte(sampleArenaJSON))
	if err != nil {
		t.Fatal(err)
	}
	d := duelConfig(cfg)
	if len(d.Teams) != 2 || d.Teams[0].Name != "Red" || d.Teams[1].Name != "Blue" {
		t.Errorf("teams: %+v", d.Teams)
	}
	if d.TeamSize != 1 || d.MinPlayers != 2 {
		t.Errorf("capacity: size=%d min=%d", d.TeamSize, d.MinPlayers)
	}
	if len(d.Generators) != 2 {
		t.Errorf("generators: %d, want 2", len(d.Generators))
	}
}

func TestArenaConfigCapacityFields(t *testing.T) {
	cfg, err := parseArenaConfig([]byte(`{"teams":[{"name":"A","beds":[{"x":0,"y":0,"z":0}]},{"name":"B","beds":[{"x":1,"y":0,"z":0}]}],"teamSize":2,"minPlayers":3}`))
	if err != nil {
		t.Fatal(err)
	}
	def, err := buildArenaDef("cap", "cap", world.NewTemplate(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if def.MinPlayers != 3 || def.MaxPlayers != 4 {
		t.Errorf("player bounds: got %d..%d, want 3..4", def.MinPlayers, def.MaxPlayers)
	}
}

// TestRealMapDuelFromConfig builds the 1×1 duel on the real DOTA map + its
// committed config: exactly two bases, on opposite sides of the map.
func TestRealMapDuelFromConfig(t *testing.T) {
	s, err := schem.LoadFile("../../" + defaultMapPath)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := s.ToTemplateAt(-int(s.Width)/2, baseY, -int(s.Length)/2)
	cfg, err := os.ReadFile("../../schem/templates/bedwars/badwars_dota_map.json")
	if err != nil {
		t.Fatal(err)
	}
	def, err := buildBedwarsDuelArenaDef("duel-real", "Duel", tmpl, cfg)
	if err != nil {
		t.Fatalf("build duel from real config: %v", err)
	}
	g := def.New().(*bedWars)
	if len(g.arena.Spawns) != 2 {
		t.Fatalf("spawns: %d, want 2", len(g.arena.Spawns))
	}
	a, b := g.arena.Spawns[0].Position, g.arena.Spawns[1].Position
	if a.Z >= 0 || b.Z <= 0 || a.X != 0 || b.X != 0 {
		t.Errorf("duel spawns should be the opposite -Z/+Z bases, got %v and %v", a, b)
	}
}
