package bedwars

import (
	"encoding/json"
	"fmt"

	"minecraft-server/game"
	"minecraft-server/world"
)

// arena_config.go builds a BedWars arena from a JSON layout that lives next to
// the .schem map. The map supplies only blocks; the config places everything
// the rules need: per-team spawns + bed blocks, resource generators, and
// villager (shop NPC) spawn points. Coordinates are world coordinates as seen
// in-game (the map is centered on the origin, so they're the F3 values).
//
// Example (badwars_dota_map.json) — villagers are per-team (each base has its
// own item/upgrade shop NPCs):
//
//	{
//	  "teams": [
//	    {"name":"Red","spawn":{"x":10,"y":65,"z":10,"yaw":90},
//	     "beds":[{"x":10,"y":65,"z":12},{"x":10,"y":65,"z":13}],
//	     "villagers":[{"type":"item","x":11,"y":65,"z":9,"yaw":180},
//	                  {"type":"upgrade","x":9,"y":65,"z":9,"yaw":180}]}
//	  ],
//	  "generators":[{"resource":"iron","x":10,"y":65,"z":8,"intervalTicks":60,"team":0},
//	                {"resource":"diamond","x":0,"y":65,"z":0}]
//	}

type vec3 struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

type arenaConfig struct {
	Teams      []teamConfig      `json:"teams"`
	Generators []generatorConfig `json:"generators"`
}

type teamConfig struct {
	Name  string `json:"name"`
	Spawn struct {
		vec3
		Yaw float32 `json:"yaw"`
	} `json:"spawn"`
	Beds []vec3 `json:"beds"`
	// Villagers are this team's shop NPCs (item / upgrade), local to its base.
	Villagers []villagerConfig `json:"villagers"`
}

type generatorConfig struct {
	Resource      string `json:"resource"`
	vec3          `json:""`
	IntervalTicks int  `json:"intervalTicks"`
	Team          *int `json:"team"` // nil → neutral; otherwise team index
}

type villagerConfig struct {
	Type string `json:"type"` // "item" / "upgrade" — label only (display-only NPC for now)
	vec3 `json:""`
	Yaw  float32 `json:"yaw"`
}

func init() {
	game.RegisterArenaBuilder("bedwars", buildBedwarsArenaDef)
}

// buildBedwarsArenaDef is the ArenaBuilder for the "bedwars" kind: parse the
// config, build the arena over a clone of the map template, and return a
// playable Definition the matchmaker can queue.
func buildBedwarsArenaDef(arenaID, name string, tmpl *world.Template, config []byte) (*game.Definition, error) {
	if len(config) == 0 {
		return nil, fmt.Errorf("missing arena config")
	}
	var cfg arenaConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parse arena config: %w", err)
	}
	if len(cfg.Teams) < 2 {
		return nil, fmt.Errorf("arena needs at least 2 teams, got %d", len(cfg.Teams))
	}
	if len(cfg.Teams) > MaxTeams {
		return nil, fmt.Errorf("arena has %d teams, max %d", len(cfg.Teams), MaxTeams)
	}

	teams := buildTeams(len(cfg.Teams))
	arena, err := buildConfigArena(tmpl, cfg, teams)
	if err != nil {
		return nil, err
	}

	const teamSize = 4 // default capacity per team until the config carries it
	return &game.Definition{
		ID:         arenaID,
		Name:       name,
		MinPlayers: 2,
		MaxPlayers: len(teams) * teamSize,
		Template:   arena.Template,
		New:        func() game.Logic { return newBedWars(arena, teams, teamSize) },
	}, nil
}

// buildConfigArena assembles an Arena from an explicit config over a clone of
// the map template (so recolouring beds doesn't mutate the shared template).
func buildConfigArena(tmpl *world.Template, cfg arenaConfig, teams []Team) (*Arena, error) {
	work := tmpl.Clone()
	a := &Arena{
		Template:  work,
		Spawns:    make([]world.SpawnPoint, len(teams)),
		BedBlocks: make([][]world.Position, len(teams)),
		bedOwner:  make(map[world.Position]int),
	}

	for i, tc := range cfg.Teams {
		a.Spawns[i] = world.SpawnPoint{
			Position: world.Position{X: tc.Spawn.X, Y: tc.Spawn.Y, Z: tc.Spawn.Z},
			Yaw:      tc.Spawn.Yaw,
		}
		if len(tc.Beds) == 0 {
			return nil, fmt.Errorf("team %d (%s) has no bed blocks", i, tc.Name)
		}
		var beds []world.Position
		for _, b := range tc.Beds {
			p := world.Position{X: b.X, Y: b.Y, Z: b.Z}
			work.SetBlock(p, teams[i].Bed)          // recolour to the team's bed
			work.AddBlockEntity(p, "minecraft:bed") // keep it visible (BER block)
			a.bedOwner[p] = i
			beds = append(beds, p)
		}
		a.BedBlocks[i] = beds

		// This team's shop NPCs (display-only for now). Stored as world
		// entities so the entity streamer shows them like any other entity.
		for _, vc := range tc.Villagers {
			work.AddEntity(world.Entity{
				Type: "minecraft:villager",
				X:    float64(vc.X) + 0.5,
				Y:    float64(vc.Y),
				Z:    float64(vc.Z) + 0.5,
				Yaw:  vc.Yaw,
			})
		}
	}

	for gi, gc := range cfg.Generators {
		res, err := parseResource(gc.Resource)
		if err != nil {
			return nil, fmt.Errorf("generator %d: %w", gi, err)
		}
		team := neutral
		if gc.Team != nil {
			if *gc.Team < 0 || *gc.Team >= len(teams) {
				return nil, fmt.Errorf("generator %d: team %d out of range", gi, *gc.Team)
			}
			team = *gc.Team
		}
		interval := uint64(defaultGenInterval(res))
		if gc.IntervalTicks > 0 {
			interval = uint64(gc.IntervalTicks)
		}
		a.Generators = append(a.Generators, Generator{
			Pos:           world.Position{X: gc.X, Y: gc.Y, Z: gc.Z},
			Resource:      res,
			IntervalTicks: interval,
			TeamID:        team,
		})
	}

	return a, nil
}

func parseResource(name string) (Resource, error) {
	switch name {
	case "iron":
		return Iron, nil
	case "gold":
		return Gold, nil
	case "diamond":
		return Diamond, nil
	case "emerald":
		return Emerald, nil
	default:
		return Iron, fmt.Errorf("unknown resource %q (want iron|gold|diamond|emerald)", name)
	}
}

func defaultGenInterval(r Resource) int {
	switch r {
	case Gold:
		return 140
	case Diamond:
		return 600
	case Emerald:
		return 900
	default: // Iron
		return 60
	}
}
