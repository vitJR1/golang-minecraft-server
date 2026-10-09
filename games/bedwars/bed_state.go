package bedwars

import (
	"strings"

	"minecraft-server/world"
)

// bed_state.go: beds are two blocks (foot + head) with a facing, and every
// colour shares the same state layout — so recolouring a bed for a team
// must carry the original facing/part over, not drop to the colour's
// default state (which is "foot, facing north": that's what made map beds
// show up as two foot halves).

// recolourBed returns orig in team colour, keeping its facing / occupied /
// part. A non-bed orig (air, a wrong block under a config position) yields
// the team's default bed state.
func recolourBed(orig world.Block, teamBed world.Block) world.Block {
	if !strings.HasSuffix(orig.Name, "_bed") {
		return teamBed
	}
	origDefault, ok := world.BlockByName(orig.Name)
	if !ok {
		return teamBed
	}
	return world.Block{StateID: teamBed.StateID + (orig.StateID - origDefault.StateID), Name: teamBed.Name}
}

// bedPair returns the foot and head states of a team bed whose head lies in
// direction facing from the foot ("north" = head at z-1).
func bedPair(teamBed world.Block, facing string) (foot, head world.Block) {
	foot = world.Block{Name: teamBed.Name, StateID: world.ResolveStateID(teamBed.Name,
		map[string]string{"facing": facing, "occupied": "false", "part": "foot"})}
	head = world.Block{Name: teamBed.Name, StateID: world.ResolveStateID(teamBed.Name,
		map[string]string{"facing": facing, "occupied": "false", "part": "head"})}
	return foot, head
}

// facingOf names the cardinal direction of an axis-aligned (dx, dz) step.
func facingOf(dx, dz int) string {
	switch {
	case dz < 0:
		return "north"
	case dz > 0:
		return "south"
	case dx < 0:
		return "west"
	default:
		return "east"
	}
}
