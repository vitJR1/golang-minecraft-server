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
//	  "generators":[{"resource":"iron","x":10,"y":65,"z":8,"intervalTicks":60,"team":0,"maxStack":48},
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

	// TeamSize caps players per team (default defaultTeamSize); MinPlayers is
	// how many the round needs before it counts (default 2). Both optional —
	// the 1×1 builder overrides them regardless of what the file says.
	TeamSize   int `json:"teamSize"`
	MinPlayers int `json:"minPlayers"`
}

const (
	// KindFull is the arena kind for the regular team mode (every team in the
	// config, up to TeamSize players each).
	KindFull = "bedwars"
	// KindDuel is the 1×1 arena kind: two opposite bases from the same map,
	// one player per team.
	KindDuel = "bedwars-1x1"

	defaultTeamSize = 4
)

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
	// MaxStack caps the uncollected pile at the generator (0 → per-resource
	// default: iron 48, gold 16, diamond 8, emerald 4).
	MaxStack int `json:"maxStack,omitempty"`
}

type villagerConfig struct {
	Type string `json:"type"` // "item" / "upgrade" — label only (display-only NPC for now)
	vec3 `json:""`
	Yaw  float32 `json:"yaw"`
}

func init() {
	game.RegisterArenaBuilder(KindFull, buildBedwarsArenaDef)
	game.RegisterArenaBuilder(KindDuel, buildBedwarsDuelArenaDef)
}

// buildBedwarsArenaDef is the ArenaBuilder for the "bedwars" kind: parse the
// config, build the arena over a clone of the map template, and return a
// playable Definition the matchmaker can queue.
func buildBedwarsArenaDef(arenaID, name string, tmpl *world.Template, config []byte) (*game.Definition, error) {
	cfg, err := parseArenaConfig(config)
	if err != nil {
		return nil, err
	}
	return buildArenaDef(arenaID, name, tmpl, cfg)
}

// buildBedwarsDuelArenaDef is the ArenaBuilder for the "bedwars-1x1" kind: the
// same map and config as the full mode, but only two bases are used (opposite
// ones on a 4-team map) and each team holds a single player. The unused
// teams' beds stay as plain map blocks (protected, not owned by anyone) and
// their villagers/generators aren't spawned.
func buildBedwarsDuelArenaDef(arenaID, name string, tmpl *world.Template, config []byte) (*game.Definition, error) {
	cfg, err := parseArenaConfig(config)
	if err != nil {
		return nil, err
	}
	full := cfg
	cfg = duelConfig(cfg)
	def, err := buildArenaDef(arenaID, name, tmpl, cfg)
	if err != nil {
		return nil, err
	}
	// The bases that aren't in play keep their map blocks — except their
	// beds, which would otherwise sit there in the map's original colour
	// looking like somebody's. Clear them so the arena shows exactly the
	// two team beds.
	for _, dropped := range droppedTeams(full, cfg) {
		for _, b := range dropped.Beds {
			pos := world.Position{X: b.X, Y: b.Y, Z: b.Z}
			def.Template.SetBlock(pos, world.Air)
			def.Template.RemoveBlockEntity(pos)
		}
	}
	return def, nil
}

// droppedTeams returns the teams of full that reduced no longer contains
// (matched by spawn position, which is unique per base).
func droppedTeams(full, reduced arenaConfig) []teamConfig {
	kept := map[vec3]bool{}
	for _, t := range reduced.Teams {
		kept[t.Spawn.vec3] = true
	}
	var out []teamConfig
	for _, t := range full.Teams {
		if !kept[t.Spawn.vec3] {
			out = append(out, t)
		}
	}
	return out
}

// parseArenaConfig decodes the JSON layout and validates the team count.
func parseArenaConfig(config []byte) (arenaConfig, error) {
	var cfg arenaConfig
	if len(config) == 0 {
		return cfg, fmt.Errorf("missing arena config")
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		return cfg, fmt.Errorf("parse arena config: %w", err)
	}
	if len(cfg.Teams) < 2 {
		return cfg, fmt.Errorf("arena needs at least 2 teams, got %d", len(cfg.Teams))
	}
	if len(cfg.Teams) > MaxTeams {
		return cfg, fmt.Errorf("arena has %d teams, max %d", len(cfg.Teams), MaxTeams)
	}
	if cfg.TeamSize <= 0 {
		cfg.TeamSize = defaultTeamSize
	}
	if cfg.MinPlayers <= 0 {
		cfg.MinPlayers = 2
	}
	return cfg, nil
}

// buildArenaDef builds the arena over a clone of the map and wraps it in a
// Definition sized from the config (Teams × TeamSize).
func buildArenaDef(arenaID, name string, tmpl *world.Template, cfg arenaConfig) (*game.Definition, error) {
	teams := buildTeams(len(cfg.Teams))
	arena, err := buildConfigArena(tmpl, cfg, teams)
	if err != nil {
		return nil, err
	}
	teamSize := cfg.TeamSize
	return &game.Definition{
		ID:         arenaID,
		Name:       name,
		MinPlayers: cfg.MinPlayers,
		MaxPlayers: len(teams) * teamSize,
		Template:   arena.Template,
		New:        func() game.Logic { return newBedWars(arena, teams, teamSize) },
	}, nil
}

// duelConfig reduces a config to the 1×1 layout: two teams of one player.
// A 2-team config is used as-is; otherwise the first team and the one
// halfway round the list are taken — configs list bases in ring order, so
// on a symmetric 4-team map that's the pair of opposite islands, giving
// both duellists the longest possible approach. Generators are re-indexed
// to the kept teams; those owned by dropped teams are removed, neutral
// ones stay.
func duelConfig(cfg arenaConfig) arenaConfig {
	keep := []int{0, 1}
	if len(cfg.Teams) > 2 {
		keep = []int{0, len(cfg.Teams) / 2}
	}
	return selectTeams(cfg, keep, 1, 2)
}

// selectTeams returns a copy of cfg with only the teams at the given config
// indices (in that order), generator team references remapped accordingly,
// and the capacity fields set. Generators owned by a dropped team are
// omitted.
func selectTeams(cfg arenaConfig, keep []int, teamSize, minPlayers int) arenaConfig {
	remap := make(map[int]int, len(keep))
	out := arenaConfig{
		Teams:      make([]teamConfig, 0, len(keep)),
		TeamSize:   teamSize,
		MinPlayers: minPlayers,
	}
	for newIdx, oldIdx := range keep {
		remap[oldIdx] = newIdx
		out.Teams = append(out.Teams, cfg.Teams[oldIdx])
	}
	for _, gc := range cfg.Generators {
		if gc.Team != nil {
			newIdx, ok := remap[*gc.Team]
			if !ok {
				continue // belongs to a base that isn't in play
			}
			t := newIdx
			gc.Team = &t
		}
		out.Generators = append(out.Generators, gc)
	}
	return out
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
			work.SetBlock(p, recolourBed(work.GetBlock(p), teams[i].Bed)) // recolour, keep facing/part
			work.AddBlockEntity(p, "minecraft:bed")                       // keep it visible (BER block)
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
			MaxStack:      gc.MaxStack,
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
