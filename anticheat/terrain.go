package anticheat

import (
	"math"
	"strings"

	"minecraft-server/world"
)

// terrain.go: the movement checks' view of the blocks — the player's box,
// walls, ground, the surfaces vanilla movement isn't plain walking on.

// BlockAt reads a block.
type BlockAt func(world.Position) world.Block

const (
	// boxEpsilon shrinks the player box for the end-of-move test, so a
	// player flush against a wall or floor doesn't count as inside it.
	boxEpsilon = 1e-3
	// pathDeflate shrinks the box tested along the path of a move: a
	// straight line between two legal positions may shave a corner that
	// vanilla's axis-by-axis collision walked around; going through a wall
	// still drags the box's core through it.
	pathDeflate = 0.2
	// pathStep is the sampling distance along a move.
	pathStep = 0.25
	// groundProbe is how far under the feet a supporting block may be.
	groundProbe = 0.05
	// groundReach widens the footprint for the ground test: a player
	// sneaking on the very edge of a block (bridging) overlaps it by a
	// hair, and "airborne" there would read as hovering.
	groundReach = 0.03
)

// PlayerBoxAt is the player's hitbox standing at feet, shrunk by deflate.
func PlayerBoxAt(feet Vec, sneaking bool, deflate float64) Box {
	h := PlayerHeight
	if sneaking {
		h = SneakHeight
	}
	return FeetBox(feet, PlayerWidth, h).Expand(-deflate)
}

func floorI(v float64) int { return int(math.Floor(v)) }

// AnyBlock reports whether some block whose cell overlaps box satisfies
// pred.
func AnyBlock(at BlockAt, box Box, pred func(world.Block) bool) bool {
	for x := floorI(box.Min.X); x <= floorI(box.Max.X); x++ {
		for y := floorI(box.Min.Y); y <= floorI(box.Max.Y); y++ {
			for z := floorI(box.Min.Z); z <= floorI(box.Max.Z); z++ {
				if pred(at(world.Position{X: x, Y: y, Z: z})) {
					return true
				}
			}
		}
	}
	return false
}

// InsideWall reports whether a box overlaps a full-cube block.
func InsideWall(at BlockAt, box Box) bool { return AnyBlock(at, box, world.FullCube) }

// Blocked reports whether a player standing at feet would be inside a wall.
func Blocked(at BlockAt, feet Vec, sneaking bool) bool {
	return InsideWall(at, PlayerBoxAt(feet, sneaking, boxEpsilon))
}

// OnGround reports whether a player standing at feet is supported: a block
// with collision under the footprint (or, for partial blocks such as slabs,
// in the cell the feet are in).
func OnGround(at BlockAt, feet Vec) bool {
	box := PlayerBoxAt(feet, false, -groundReach)
	box.Max.Y = feet.Y
	box.Min.Y = feet.Y - groundProbe
	return AnyBlock(at, box, world.Collidable)
}

// Phased reports whether moving from `from` to `to` takes the player into or
// through a full block: the end box overlaps one, or the shrunken box does
// somewhere along the way. A player already inside a block (a game built
// around them) is left alone.
func Phased(at BlockAt, from, to Vec, sneaking bool) bool {
	if Blocked(at, from, sneaking) {
		return false
	}
	if Blocked(at, to, sneaking) {
		return true
	}
	dist := Dist(from, to)
	steps := int(math.Ceil(dist / pathStep))
	for k := 1; k < steps; k++ {
		f := float64(k) / float64(steps)
		p := Vec{X: from.X + (to.X-from.X)*f, Y: from.Y + (to.Y-from.Y)*f, Z: from.Z + (to.Z-from.Z)*f}
		if InsideWall(at, PlayerBoxAt(p, sneaking, pathDeflate)) {
			return true
		}
	}
	return false
}

// BlocksUnder lists the blocks under the player's footprint at feet and in
// the cell the feet are in (beds and slabs are lower than a cell).
func BlocksUnder(at BlockAt, feet Vec) []world.Block {
	box := PlayerBoxAt(feet, false, boxEpsilon)
	box.Max.Y = feet.Y
	box.Min.Y = feet.Y - groundProbe
	var out []world.Block
	AnyBlock(at, box, func(b world.Block) bool { out = append(out, b); return false })
	return out
}

// MotionExempt reports whether the player is somewhere vanilla movement
// isn't plain walking and falling: in a fluid, on something climbable, in
// cobweb, against honey, in powder snow or scaffolding.
func MotionExempt(at BlockAt, feet Vec, sneaking bool) bool {
	box := PlayerBoxAt(feet, sneaking, 0).Expand(0.3)
	return AnyBlock(at, box, func(b world.Block) bool {
		if b.Name == world.Water.Name || b.Name == world.Lava.Name {
			return true
		}
		switch n := strings.TrimPrefix(b.Name, "minecraft:"); {
		case n == "ladder", n == "vine", n == "scaffolding", n == "cobweb",
			n == "honey_block", n == "powder_snow", n == "bubble_column",
			strings.HasSuffix(n, "_vines"), strings.HasSuffix(n, "_vines_plant"):
			return true
		}
		return false
	})
}
