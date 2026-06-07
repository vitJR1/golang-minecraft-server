package bedwars

import (
	"encoding/json"
	"os"
	"testing"
)

// TestGenSampleConfig regenerates a starter arena config from the auto-detected
// layout of the default map, so /arena create has something to load. Run
// explicitly: go test ./games/bedwars/ -run TestGenSampleConfig -gen.
func TestGenSampleConfig(t *testing.T) {
	if os.Getenv("GENCFG") == "" {
		t.Skip("set GENCFG=1 to regenerate the sample arena config")
	}
	teams := buildTeams(4)
	a, err := buildSchemArena("../../"+defaultMapPath, teams)
	if err != nil {
		t.Fatal(err)
	}
	type vc = vec3
	cfg := arenaConfig{}
	for i := range teams {
		var tc teamConfig
		tc.Name = teams[i].Name
		sp := a.Spawns[i]
		tc.Spawn.X, tc.Spawn.Y, tc.Spawn.Z, tc.Spawn.Yaw = sp.Position.X, sp.Position.Y, sp.Position.Z, sp.Yaw
		for _, b := range a.BedBlocks[i] {
			tc.Beds = append(tc.Beds, vc{X: b.X, Y: b.Y, Z: b.Z})
		}
		// Starter per-team shop villagers, flanking the spawn (edit to taste).
		item := villagerConfig{Type: "item", Yaw: sp.Yaw}
		item.X, item.Y, item.Z = sp.Position.X+1, sp.Position.Y, sp.Position.Z
		upg := villagerConfig{Type: "upgrade", Yaw: sp.Yaw}
		upg.X, upg.Y, upg.Z = sp.Position.X-1, sp.Position.Y, sp.Position.Z
		tc.Villagers = []villagerConfig{item, upg}
		cfg.Teams = append(cfg.Teams, tc)
	}
	for _, g := range a.Generators {
		gc := generatorConfig{Resource: g.Resource.String(), IntervalTicks: int(g.IntervalTicks)}
		gc.X, gc.Y, gc.Z = g.Pos.X, g.Pos.Y, g.Pos.Z
		if g.TeamID != neutral {
			tid := g.TeamID
			gc.Team = &tid
		}
		// resource string lower-case for our parser
		switch gc.Resource {
		case "Iron":
			gc.Resource = "iron"
		case "Gold":
			gc.Resource = "gold"
		case "Diamond":
			gc.Resource = "diamond"
		case "Emerald":
			gc.Resource = "emerald"
		}
		cfg.Generators = append(cfg.Generators, gc)
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	path := "../../schem/templates/bedwars/badwars_dota_map.json"
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d teams, %d generators)", path, len(cfg.Teams), len(cfg.Generators))
}
