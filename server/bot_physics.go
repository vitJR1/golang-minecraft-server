package server

import (
	"math"
	"strings"

	"minecraft-server/anticheat"
	"minecraft-server/world"
)

// bot_physics.go moves bots the way a vanilla 1.20.1 client moves its
// player — a bot has no client, so the server runs its physics: walking
// acceleration and block friction, sprint, jump, gravity and drag, pushes
// from knockback / explosions, and collision with the blocks around it
// (resolved Y, then X, then Z, with the 0.6-block auto-step).
//
// The functions here only read blocks through a blockAt func, so they run
// against a test world as well as an instance.

// blockAt returns the block at a position.
type blockAt func(world.Position) world.Block

// Vanilla movement constants (LivingEntity.travel, Player).
const (
	botGravity     = 0.08
	botDrag        = 0.98
	botAirFriction = 0.91
	botWalkSpeed   = 0.1    // generic.movement_speed of a player
	botSprintMul   = 1.3    // sprinting multiplies it by 1.3
	botAirAccel    = 0.02   // flying speed: acceleration while airborne
	botGroundCoeff = 0.216  // 0.21600002 / slipperiness³ scales ground acceleration
	botJumpSpeed   = 0.42   // jump velocity (+0.1 per Jump Boost level)
	botSprintJump  = 0.2    // forward boost of a sprint-jump
	botStepHeight  = 0.6    // auto-step over slabs, stairs, carpets
	botEpsilon     = 1e-7   // collision slack
	botMaxFall     = -3.92  // terminal fall speed (blocks/tick)
	botVoidY       = -128.0 // below this the bot stops simulating a fall
	defaultSlip    = 0.6    // most blocks' slipperiness
	botStandWidth  = 0.6    // anticheat.PlayerWidth, repeated for clarity
	botStandHeight = 1.8    // anticheat.PlayerHeight
	botSneakHeight = 1.5    // anticheat.SneakHeight
	botHalfWidth   = botStandWidth / 2
)

// physState is a bot's position and motion.
type physState struct {
	Pos       anticheat.Vec // feet
	Vel       anticheat.Vec // blocks/tick
	OnGround  bool
	Collided  bool // the last horizontal move ran into something
	Sneaking  bool
	SpeedLvl  int // Speed effect level
	JumpLvl   int // Jump Boost level
	jumpDelay int // ticks before another jump (vanilla's 10-tick hold delay)
}

// physInput is what the bot "presses" this tick: a horizontal direction to
// walk in (unit, or zero to stand), sprint and jump.
type physInput struct {
	DirX, DirZ float64
	Sprint     bool
	Jump       bool
}

// slipperiness is how slippery the block under the feet is.
func slipperiness(b world.Block) float64 {
	switch b.Name {
	case "minecraft:ice", "minecraft:packed_ice", "minecraft:frosted_ice":
		return 0.98
	case "minecraft:blue_ice":
		return 0.989
	case "minecraft:slime_block":
		return 0.8
	}
	return defaultSlip
}

// botBox is the player hitbox at feet.
func botBox(feet anticheat.Vec, sneaking bool) anticheat.Box {
	h := botStandHeight
	if sneaking {
		h = botSneakHeight
	}
	return anticheat.FeetBox(feet, botStandWidth, h)
}

// collisionBoxes lists the collision boxes of the blocks overlapping region.
func collisionBoxes(at blockAt, region anticheat.Box) []anticheat.Box {
	var out []anticheat.Box
	for x := floorF(region.Min.X); x <= floorF(region.Max.X); x++ {
		for y := floorF(region.Min.Y) - 1; y <= floorF(region.Max.Y); y++ { // -1: fences reach up 1.5
			for z := floorF(region.Min.Z); z <= floorF(region.Max.Z); z++ {
				lo, hi, ok := world.CollisionHeight(at(world.Position{X: x, Y: y, Z: z}))
				if !ok {
					continue
				}
				fx, fy, fz := float64(x), float64(y), float64(z)
				b := anticheat.Box{Min: anticheat.Vec{X: fx, Y: fy + lo, Z: fz}, Max: anticheat.Vec{X: fx + 1, Y: fy + hi, Z: fz + 1}}
				if b.Max.Y > region.Min.Y && b.Min.Y < region.Max.Y {
					out = append(out, b)
				}
			}
		}
	}
	return out
}

// axis helpers: 0 = X, 1 = Y, 2 = Z.
func vecAxis(v anticheat.Vec, a int) float64 {
	switch a {
	case 0:
		return v.X
	case 1:
		return v.Y
	}
	return v.Z
}

func overlapsOn(a, b anticheat.Box, axis int) bool {
	lo1, hi1 := vecAxis(a.Min, axis), vecAxis(a.Max, axis)
	lo2, hi2 := vecAxis(b.Min, axis), vecAxis(b.Max, axis)
	return hi1 > lo2+botEpsilon && lo1 < hi2-botEpsilon
}

// clip shortens a move of d along axis so box stops at the first block box
// in the way (vanilla Shapes.collide, one axis).
func clip(boxes []anticheat.Box, box anticheat.Box, axis int, d float64) float64 {
	o1, o2 := (axis+1)%3, (axis+2)%3
	for _, b := range boxes {
		if !overlapsOn(box, b, o1) || !overlapsOn(box, b, o2) {
			continue
		}
		if d > 0 {
			if gap := vecAxis(b.Min, axis) - vecAxis(box.Max, axis); gap >= -botEpsilon && gap < d {
				d = math.Max(0, gap)
			}
		} else if d < 0 {
			if gap := vecAxis(b.Max, axis) - vecAxis(box.Min, axis); gap <= botEpsilon && gap > d {
				d = math.Min(0, gap)
			}
		}
	}
	return d
}

// shift moves a box along one axis.
func shift(b anticheat.Box, axis int, d float64) anticheat.Box {
	switch axis {
	case 0:
		b.Min.X, b.Max.X = b.Min.X+d, b.Max.X+d
	case 1:
		b.Min.Y, b.Max.Y = b.Min.Y+d, b.Max.Y+d
	default:
		b.Min.Z, b.Max.Z = b.Min.Z+d, b.Max.Z+d
	}
	return b
}

// collide resolves the move (dx, dy, dz) of box against the blocks: Y
// first, then the larger horizontal axis, then the other. It returns the
// allowed move.
func collide(at blockAt, box anticheat.Box, dx, dy, dz float64) (float64, float64, float64) {
	region := anticheat.Box{
		Min: anticheat.Vec{X: math.Min(box.Min.X, box.Min.X+dx), Y: math.Min(box.Min.Y, box.Min.Y+dy), Z: math.Min(box.Min.Z, box.Min.Z+dz)},
		Max: anticheat.Vec{X: math.Max(box.Max.X, box.Max.X+dx), Y: math.Max(box.Max.Y, box.Max.Y+dy), Z: math.Max(box.Max.Z, box.Max.Z+dz)},
	}
	boxes := collisionBoxes(at, region.Expand(botEpsilon))
	dy = clip(boxes, box, 1, dy)
	box = shift(box, 1, dy)
	if math.Abs(dx) >= math.Abs(dz) {
		dx = clip(boxes, box, 0, dx)
		box = shift(box, 0, dx)
		dz = clip(boxes, box, 2, dz)
	} else {
		dz = clip(boxes, box, 2, dz)
		box = shift(box, 2, dz)
		dx = clip(boxes, box, 0, dx)
	}
	return dx, dy, dz
}

// move applies one tick's motion with collision and the auto-step, and
// updates OnGround / Collided. Velocity components stopped by a block are
// zeroed, like vanilla.
func (s *physState) move(at blockAt) {
	box := botBox(s.Pos, s.Sneaking)
	dx, dy, dz := collide(at, box, s.Vel.X, s.Vel.Y, s.Vel.Z)
	horizBlocked := math.Abs(dx-s.Vel.X) > botEpsilon || math.Abs(dz-s.Vel.Z) > botEpsilon
	landing := s.Vel.Y < 0 && math.Abs(dy-s.Vel.Y) > botEpsilon

	// Auto-step: on the ground (or landing) and stopped sideways — try the
	// move again from up to botStepHeight higher, keep it if it gets
	// further.
	if horizBlocked && (s.OnGround || landing) {
		_, up, _ := collide(at, box, 0, botStepHeight, 0)
		raised := shift(box, 1, up)
		sx, _, sz := collide(at, raised, s.Vel.X, 0, s.Vel.Z)
		if sx*sx+sz*sz > dx*dx+dz*dz+botEpsilon {
			raised = shift(shift(raised, 0, sx), 2, sz)
			_, down, _ := collide(at, raised, 0, -up, 0)
			dx, dy, dz = sx, up+down, sz
			horizBlocked = math.Abs(dx-s.Vel.X) > botEpsilon || math.Abs(dz-s.Vel.Z) > botEpsilon
		}
	}

	s.OnGround = s.Vel.Y < 0 && math.Abs(dy-s.Vel.Y) > botEpsilon || (s.OnGround && dy == 0 && s.Vel.Y <= 0)
	s.Collided = horizBlocked
	if math.Abs(dx-s.Vel.X) > botEpsilon {
		s.Vel.X = 0
	}
	if math.Abs(dz-s.Vel.Z) > botEpsilon {
		s.Vel.Z = 0
	}
	if math.Abs(dy-s.Vel.Y) > botEpsilon {
		s.Vel.Y = 0
	}
	s.Pos = anticheat.Vec{X: s.Pos.X + dx, Y: s.Pos.Y + dy, Z: s.Pos.Z + dz}
}

// step runs one tick of vanilla player physics with input in.
func (s *physState) step(at blockAt, in physInput) {
	if s.jumpDelay > 0 {
		s.jumpDelay--
	}
	under := at(world.Position{X: floorF(s.Pos.X), Y: floorF(s.Pos.Y - 0.5000001), Z: floorF(s.Pos.Z)})
	slip := slipperiness(under)

	// Jump.
	if in.Jump && s.OnGround && s.jumpDelay == 0 {
		s.Vel.Y = botJumpSpeed + 0.1*float64(s.JumpLvl)
		if in.Sprint {
			s.Vel.X += in.DirX * botSprintJump
			s.Vel.Z += in.DirZ * botSprintJump
		}
		s.OnGround = false
		s.jumpDelay = 10
	}

	// Walking acceleration (moveRelative).
	speed := botWalkSpeed * (1 + 0.2*float64(s.SpeedLvl))
	if in.Sprint {
		speed *= botSprintMul
	}
	accel := botAirAccel
	if in.Sprint {
		accel *= botSprintMul
	}
	if s.OnGround {
		accel = speed * botGroundCoeff / (slip * slip * slip)
	}
	if s.Sneaking {
		accel *= 0.3
	}
	s.Vel.X += in.DirX * accel
	s.Vel.Z += in.DirZ * accel

	s.move(at)

	// Gravity, drag and friction for the next tick.
	friction := botAirFriction
	if s.OnGround {
		friction = slip * botAirFriction
	}
	s.Vel.Y = math.Max(botMaxFall, (s.Vel.Y-botGravity)*botDrag)
	s.Vel.X *= friction
	s.Vel.Z *= friction
}

// push applies a velocity the server gave the player: knockback replaces
// the motion, as Set Entity Velocity does on a client.
func (s *physState) push(vx, vy, vz float64) {
	s.Vel = anticheat.Vec{X: vx, Y: vy, Z: vz}
	if vy > 0 {
		s.OnGround = false
	}
}

// isWool reports whether an item id is any colour of wool.
func isWool(item string) bool { return strings.HasSuffix(item, "_wool") }
