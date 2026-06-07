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
