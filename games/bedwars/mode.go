package bedwars

import "fmt"

// Mode is a registerable BedWars preset: how many teams, how big each team
// is, and how few players can start a round. One Mode → one entry in the
// matchmaker (its own game ID, its own arena). This is the single place to
// add a new variant — e.g. 2 teams of 5 — without touching the game loop,
// the arena builder, or the team logic.
//
//	{ID: "bedwars-2x5", Name: "BedWars 2×5", Teams: 2, TeamSize: 5}
//
// MaxPlayers is derived (Teams × TeamSize), so the matchmaker queue and the
// per-team capacity can never disagree.
type Mode struct {
	ID       string // matchmaker/game ID, e.g. "bedwars" or "bedwars-2x5"
	Name     string // display name
	Teams    int    // number of teams (1..MaxTeams)
	TeamSize int    // max players per team
	MinStart int    // min players to start a round (defaults to Teams)

	// Map is an optional path to a .schem file to play on. When set, the
	// arena is built from that map (beds detected from the blocks, recoloured
	// per team) instead of being procedurally generated. The file must
	// contain exactly Teams beds; if it can't be loaded or doesn't match, the
	// mode falls back to the generated arena (logged, not fatal).
	Map string
}

// maxPlayers is the total lobby capacity for the mode.
func (m Mode) maxPlayers() int { return m.Teams * m.TeamSize }

// minPlayers is how many players the matchmaker waits for. Defaults to one
// per team (so every team can have someone) but never below 2.
func (m Mode) minPlayers() int {
	if m.MinStart > 0 {
		return m.MinStart
	}
	if m.Teams >= 2 {
		return m.Teams
	}
	return 2
}

// validate rejects nonsensical presets at startup (fail loud, like the rest
// of registration).
func (m Mode) validate() error {
	if m.ID == "" {
		return fmt.Errorf("bedwars mode: empty ID")
	}
	if m.Teams < 1 || m.Teams > MaxTeams {
		return fmt.Errorf("bedwars mode %q: Teams=%d out of range 1..%d", m.ID, m.Teams, MaxTeams)
	}
	if m.TeamSize < 1 {
		return fmt.Errorf("bedwars mode %q: TeamSize=%d must be ≥1", m.ID, m.TeamSize)
	}
	return nil
}

// defaultModes is the set registered at startup. Add a line here to ship a
// new variant; nothing else needs to change.
// defaultMapPath is the shipped 4-team map, relative to the server's working
// directory (the repo root, same base as templatesRoot in main.go).
const defaultMapPath = "schem/templates/bedwars/badwars_dota_map.schem"

var defaultModes = []Mode{
	// MinStart:2 keeps the flagship mode testable with just two clients;
	// drop it to require one player per team before a round begins.
	{ID: "bedwars", Name: "BedWars 4×4", Teams: 4, TeamSize: 4, MinStart: 2, Map: defaultMapPath},
	// Example of the variant you want next — 2 teams of 5. Uncomment (or
	// add your own) and it appears in /play with its own arena. Omit Map to
	// use the generated arena, or point it at a 2-bed schematic:
	// {ID: "bedwars-2x5", Name: "BedWars 2×5", Teams: 2, TeamSize: 5, MinStart: 2},
}
