package bedwars

import "minecraft-server/world"

// Team is the static identity of one team in a round: its slot index and
// its colours. Where the island physically sits is NOT stored here — the
// arena builder derives that from the team's index and the total team
// count, so the same Team definition works whether there are 2 teams or 8
// (Open/Closed: new layouts don't touch this type).
type Team struct {
	ID   int    // slice index; also the matchmaking colour slot
	Name string // human name shown in chat ("Red", "Blue", …)

	// Wool clads the island; Bed is the respawn anchor to defend.
	Wool world.Block
	Bed  world.Block
}

// colorPalette is the ordered pool of team colours. A mode with N teams
// takes the first N entries, so 2-team modes are Red/Blue, 4-team adds
// Green/Yellow, and so on up to the palette length. Append here to support
// more simultaneous teams.
var colorPalette = []struct {
	name string
	wool world.Block
	bed  world.Block
}{
	{"Red", world.RedWool, world.RedBed},
	{"Blue", world.BlueWool, world.BlueBed},
	{"Green", world.GreenWool, world.GreenBed},
	{"Yellow", world.YellowWool, world.YellowBed},
	{"Cyan", world.CyanWool, world.CyanBed},
	{"White", world.WhiteWool, world.WhiteBed},
	{"Pink", world.PinkWool, world.PinkBed},
	{"Gray", world.GrayWool, world.GrayBed},
}

// MaxTeams is the largest team count any mode can request (bounded by the
// colour palette).
var MaxTeams = len(colorPalette)

// buildTeams returns the first n teams from the palette. Panics if n is out
// of range — modes are validated at registration, so this is a programmer
// error, not a runtime condition.
func buildTeams(n int) []Team {
	if n < 1 || n > len(colorPalette) {
		panic("bedwars: team count out of range")
	}
	teams := make([]Team, n)
	for i := range n {
		c := colorPalette[i]
		teams[i] = Team{ID: i, Name: c.name, Wool: c.wool, Bed: c.bed}
	}
	return teams
}

// teamState is one team's mutable per-round state. members holds the entity
// IDs of players currently on the team who are still in the game (a player
// removed from members is either disconnected or eliminated).
type teamState struct {
	team     Team
	bedAlive bool
	members  map[int32]bool // active players' entity IDs
}

func newTeamState(t Team) *teamState {
	return &teamState{team: t, bedAlive: true, members: make(map[int32]bool)}
}

// inPlay reports whether the team can still win: it has at least one active
// member. A team with a dead bed is still in play until its last member
// dies; a team that has lost its bed AND all members is out.
func (s *teamState) inPlay() bool { return len(s.members) > 0 }
