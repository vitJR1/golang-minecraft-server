package server

import (
	"fmt"
	"math"
	"strings"
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
	// groundReach widens the footprint for the ground test: a player
	// sneaking on the very edge of a block (bridging) overlaps it by a
	// hair, and "airborne" there would read as hovering.
	groundReach = 0.03
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
	box := playerBoxAt(feet, false, -groundReach)
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

// --- stage two: speed and flight -----------------------------------------

const (
	// maxHorizontal is the horizontal distance per move a vanilla client
	// covers without help: a sprint-jump peaks around 0.62 blocks/tick.
	maxHorizontal = 0.75
	// iceBonus / blueIceBonus: ice keeps momentum, sprint-jumping on it
	// goes much faster (blue ice fastest).
	iceBonus     = 1.0
	blueIceBonus = 1.6
	// iceMemory / bounceMemory: moves after leaving ice / a bouncy block
	// that still carry its effect (2 s, 3 s).
	iceMemory    = 40
	bounceMemory = 60
	// effectHold: moves an ended Speed / Jump Boost is still allowed for —
	// the client stops using it only when the removal packet arrives.
	effectHold = 40
	// speedBufferMax is how much excess distance may pile up before it's a
	// violation; speedBufferDecay drains it on every move within limits.
	speedBufferMax   = 0.3
	speedBufferDecay = 0.05
	// flyMargin is added to the computed apex.
	flyMargin = 0.2
	// Hover: after hoverAfter airborne moves (past any jump's apex) a move
	// that doesn't fall by more than hoverDY counts as stalled, and after
	// glideAfter moves anything slower than glideDY does (free fall is long
	// past -1 block/tick by then); hoverMoves stalled moves in a row fly.
	hoverAfter = 15
	hoverDY    = 0.03
	glideAfter = 30
	glideDY    = 0.3
	hoverMoves = 10
)

// landedAt starts the air state over at height y: the player stands there
// as far as flight checks go (ground under the feet, a ladder, water).
func (c *ClientConnection) landedAt(y float64) {
	m := &c.ac.Move
	m.GroundY, m.HasGround = y, true
	m.AirMoves, m.Stalled = 0, 0
}

// teleported starts every movement check over at a real (non-setback)
// teleport: new ground, no speed excess carried from the old place.
func (c *ClientConnection) teleported(y float64) {
	c.landedAt(y)
	c.ac.Move.SpeedBuffer = 0
}

// blocksUnder calls fn for the blocks under the player's footprint at feet
// and in the cell the feet are in (beds and slabs are lower than a cell).
func (i *Instance) blocksUnder(feet anticheat.Vec, fn func(world.Block)) {
	box := playerBoxAt(feet, false, boxEpsilon)
	box.Max.Y = feet.Y
	box.Min.Y = feet.Y - groundProbe
	i.anyBlock(box, func(b world.Block) bool { fn(b); return false })
}

// motionExempt reports whether the player is somewhere vanilla movement
// isn't plain walking and falling: in a fluid, on something climbable, in
// cobweb, against honey, in powder snow or scaffolding.
func (i *Instance) motionExempt(feet anticheat.Vec, sneaking bool) bool {
	box := playerBoxAt(feet, sneaking, 0).Expand(0.3)
	return i.anyBlock(box, func(b world.Block) bool {
		if _, _, fluid := fluidOf(b); fluid {
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

// checkMotion is the speed and flight check for an accepted (non-phasing)
// move from → to. It returns the violated check and its evidence, or "".
// Creative and spectator players fly legitimately and are skipped.
func (c *ClientConnection) checkMotion(from, to anticheat.Vec) (check, detail string) {
	gm := c.gamemode()
	if gm == player.Creative || gm == player.Spectator || c.player.IsDead() {
		c.landedAt(to.Y)
		return "", ""
	}
	inst, m := c.instance, &c.ac.Move
	ground := inst.onGroundAt(to)
	defer c.ac.Velocity.Moved(ground)

	m.IceLeft = max(0, m.IceLeft-1)
	m.BounceLeft = max(0, m.BounceLeft-1)
	inst.blocksUnder(to, func(b world.Block) {
		switch n := b.Name; {
		case strings.HasSuffix(n, "ice"):
			m.IceLeft, m.BlueIce = iceMemory, n == "minecraft:blue_ice"
		case n == "minecraft:slime_block", strings.HasSuffix(n, "_bed"):
			m.BounceLeft = bounceMemory
		}
	})
	speedLvl := anticheat.Held(c.effectLevel(EffectSpeed), &m.SpeedLevel, &m.SpeedHold, effectHold)
	jumpLvl := anticheat.Held(c.effectLevel(EffectJumpBoost), &m.JumpLevel, &m.JumpHold, effectHold)
	pushH, pushUp := c.ac.Velocity.Allowance()

	// Horizontal.
	h := math.Hypot(to.X-from.X, to.Z-from.Z)
	limit := maxHorizontal*(1+speedPerLevel*float64(speedLvl)) + pushH // speedPerLevel: attributes.go
	if m.IceLeft > 0 {
		if m.BlueIce {
			limit += blueIceBonus
		} else {
			limit += iceBonus
		}
	}
	if h > limit {
		m.SpeedBuffer += h - limit
		if m.SpeedBuffer > speedBufferMax {
			m.SpeedBuffer = 0
			return "movement.speed", fmt.Sprintf("%.2f blocks in one move, limit %.2f", h, limit)
		}
	} else {
		m.SpeedBuffer = math.Max(0, m.SpeedBuffer-speedBufferDecay)
	}

	// Vertical.
	if ground || !m.HasGround || m.BounceLeft > 0 || inst.motionExempt(to, c.ac.Sneaking.Load()) {
		c.landedAt(to.Y)
		return "", ""
	}
	m.AirMoves++
	rise := to.Y - m.GroundY
	apex := anticheat.Apex(anticheat.JumpVelocity(jumpLvl)) + anticheat.Apex(pushUp) + flyMargin
	if rise > apex {
		return "movement.fly", fmt.Sprintf("%.2f above the last ground, limit %.2f", rise, apex)
	}
	dy := to.Y - from.Y
	stalled := (m.AirMoves > hoverAfter && dy > -hoverDY) || (m.AirMoves > glideAfter && dy > -glideDY)
	if stalled && pushUp == 0 {
		m.Stalled++
	} else {
		m.Stalled = 0
	}
	if m.Stalled >= hoverMoves {
		m.Stalled = 0
		return "movement.fly", fmt.Sprintf("%d airborne moves without falling (dy %.3f)", hoverMoves, dy)
	}
	return "", ""
}
