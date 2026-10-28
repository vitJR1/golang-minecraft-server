package server

import (
	"fmt"
	"math"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/world"
)

// movement.go: server-side movement checks (anticheat.md #9), stage one —
// timer, phase, and the server's own idea of "on the ground". Speed and fly
// build on these.

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
)

// countMove feeds one movement packet to the timer check.
func (c *ClientConnection) countMove() {
	if ahead, flagged := c.ac.Timer.Move(time.Now()); flagged {
		c.flag("movement.timer", fmt.Sprintf("%d ms ahead of real time", ahead.Milliseconds()))
	}
}

// playerBoxAt is the player's hitbox standing at feet, shrunk by deflate.
func playerBoxAt(feet anticheat.Vec, sneaking bool, deflate float64) anticheat.Box {
	h := anticheat.PlayerHeight
	if sneaking {
		h = anticheat.SneakHeight
	}
	return anticheat.FeetBox(feet, anticheat.PlayerWidth, h).Expand(-deflate)
}

// anyBlock reports whether some block whose cell overlaps box satisfies
// pred.
func (i *Instance) anyBlock(box anticheat.Box, pred func(world.Block) bool) bool {
	for x := floorF(box.Min.X); x <= floorF(box.Max.X); x++ {
		for y := floorF(box.Min.Y); y <= floorF(box.Max.Y); y++ {
			for z := floorF(box.Min.Z); z <= floorF(box.Max.Z); z++ {
				if pred(i.World.GetBlock(world.Position{X: x, Y: y, Z: z})) {
					return true
				}
			}
		}
	}
	return false
}

// insideWall reports whether a box overlaps a full-cube block.
func (i *Instance) insideWall(box anticheat.Box) bool {
	return i.anyBlock(box, world.FullCube)
}

// onGroundAt reports whether a player standing at feet is supported: a block
// with collision under the footprint (or, for partial blocks such as slabs,
// in the cell the feet are in).
func (i *Instance) onGroundAt(feet anticheat.Vec) bool {
	box := playerBoxAt(feet, false, boxEpsilon)
	box.Max.Y = feet.Y
	box.Min.Y = feet.Y - groundProbe
	return i.anyBlock(box, world.Collidable)
}

// phased reports whether moving from `from` to `to` takes the player into or
// through a full block: the end box overlaps one, or the shrunken box does
// somewhere along the way. A player already inside a block (a game built
// around them) is left alone, as is a spectator.
func (c *ClientConnection) phased(from, to anticheat.Vec) bool {
	if c.gamemode() == player.Spectator {
		return false
	}
	sneak := c.ac.Sneaking.Load()
	inst := c.instance
	if inst.insideWall(playerBoxAt(from, sneak, boxEpsilon)) {
		return false
	}
	if inst.insideWall(playerBoxAt(to, sneak, boxEpsilon)) {
		return true
	}
	dist := anticheat.Dist(from, to)
	steps := int(math.Ceil(dist / pathStep))
	for k := 1; k < steps; k++ {
		f := float64(k) / float64(steps)
		p := anticheat.Vec{X: from.X + (to.X-from.X)*f, Y: from.Y + (to.Y-from.Y)*f, Z: from.Z + (to.Z-from.Z)*f}
		if inst.insideWall(playerBoxAt(p, sneak, pathDeflate)) {
			return true
		}
	}
	return false
}

// moved records an accepted move from → to in the server's movement state.
func (c *ClientConnection) moved(from, to anticheat.Vec) {
	c.ac.Move.LastDY = to.Y - from.Y
}

// serverOnGround is the server's ground state, worked out from the blocks
// under the player's position, for gameplay that used to trust the packet's
// onGround flag — a free "critical hit on every swing" switch for a
// modified client (critical hits, the airborne dig penalty).
func (c *ClientConnection) serverOnGround() bool {
	if c.instance == nil || c.player == nil {
		return true
	}
	return c.instance.onGroundAt(c.feet())
}
