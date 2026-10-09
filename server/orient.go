package server

import (
	"math"

	"minecraft-server/world"
)

// orient.go picks the block state a placed block should get from HOW it was
// placed — the clicked face and the player's look direction — for blocks
// whose default state would be wrong or unattached. Today: ladders.

// faceName maps a UseItemOn face index (2..5) to its facing property value.
func faceName(face int) (string, bool) {
	switch face {
	case 2:
		return "north", true
	case 3:
		return "south", true
	case 4:
		return "west", true
	case 5:
		return "east", true
	}
	return "", false
}

// horizontalFacing is the cardinal direction the player looks along.
func horizontalFacing(yaw float32) string {
	bucket := (int(math.Floor(float64(yaw)/90+0.5))%4 + 4) % 4
	switch bucket {
	case 0:
		return "south"
	case 1:
		return "west"
	case 2:
		return "north"
	default:
		return "east"
	}
}

func opposite(dir string) string {
	switch dir {
	case "north":
		return "south"
	case "south":
		return "north"
	case "west":
		return "east"
	default:
		return "west"
	}
}

func step(dir string) (dx, dz int) {
	switch dir {
	case "north":
		return 0, -1
	case "south":
		return 0, 1
	case "west":
		return -1, 0
	default:
		return 1, 0
	}
}

// orientForPlacement returns the state blk should be placed with at pos,
// given the clicked face and the placer's yaw, and whether the placement is
// valid at all. Blocks without placement rules pass through unchanged.
func (i *Instance) orientForPlacement(blk world.Block, pos world.Position, face int, yaw float32) (world.Block, bool) {
	switch blk.Name {
	case world.Ladder.Name:
		return i.orientLadder(blk, pos, face, yaw)
	}
	return blk, true
}

// orientLadder hangs the ladder on a wall: on the clicked side when that is
// a horizontal face with a solid block behind, otherwise on the first solid
// wall around pos starting from the one the player faces (vanilla's
// getStateForPlacement). No wall → can't place.
func (i *Instance) orientLadder(blk world.Block, pos world.Position, face int, yaw float32) (world.Block, bool) {
	var candidates []string
	if f, ok := faceName(face); ok {
		candidates = append(candidates, f)
	}
	look := horizontalFacing(yaw)
	for _, d := range []string{opposite(look), look, "north", "south", "west", "east"} {
		candidates = append(candidates, d)
	}
	for _, facing := range candidates {
		// The ladder faces away from its wall: the wall is behind it.
		dx, dz := step(opposite(facing))
		wall := i.World.GetBlock(world.Position{X: pos.X + dx, Y: pos.Y, Z: pos.Z + dz})
		if _, _, fluid := fluidOf(wall); wall == world.Air || fluid {
			continue
		}
		blk.StateID = world.ResolveStateID(blk.Name, map[string]string{"facing": facing, "waterlogged": "false"})
		return blk, true
	}
	return blk, false
}
