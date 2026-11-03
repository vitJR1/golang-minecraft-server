package anticheat

import (
	"fmt"
	"math"
	"strings"

	"minecraft-server/world"
)

// motion.go: the speed and flight check over the movement state (anticheat.md
// #9, stage two).

const (
	// maxHorizontal is the horizontal distance per move a vanilla client
	// covers without help: a sprint-jump peaks around 0.62 blocks/tick.
	maxHorizontal = 0.75
	// speedPerLevel is Speed's +20% movement per level (vanilla
	// MobEffects.MOVEMENT_SPEED, the modifier server/gameplay/attributes sends).
	speedPerLevel = 0.2
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

// Moved records an accepted move from → to.
func (m *MoveState) Moved(from, to Vec) {
	m.LastDY = to.Y - from.Y
	m.PrevFeet, m.HasPrev = from, true
}

// Landed starts the air state over at height y: the player stands there as
// far as flight checks go (ground under the feet, a ladder, water).
func (m *MoveState) Landed(y float64) {
	m.GroundY, m.HasGround = y, true
	m.AirMoves, m.Stalled = 0, 0
}

// Teleported starts every movement check over at a real (non-setback)
// teleport: new ground, no speed excess carried from the old place.
func (m *MoveState) Teleported(y float64) {
	m.Landed(y)
	m.SpeedBuffer = 0
}

// NoteSurface counts down ice and bounce momentum and renews it from the
// blocks under the feet (BlocksUnder).
func (m *MoveState) NoteSurface(under []world.Block) {
	m.IceLeft = max(0, m.IceLeft-1)
	m.BounceLeft = max(0, m.BounceLeft-1)
	for _, b := range under {
		switch n := b.Name; {
		case strings.HasSuffix(n, "ice"):
			m.IceLeft, m.BlueIce = iceMemory, n == "minecraft:blue_ice"
		case n == "minecraft:slime_block", strings.HasSuffix(n, "_bed"):
			m.BounceLeft = bounceMemory
		}
	}
}

// Motion is one move as the speed / flight check sees it.
type Motion struct {
	From, To Vec
	// Ground: supported at To (OnGround); Exempt: in a fluid, on a ladder…
	// (MotionExempt).
	Ground, Exempt bool
	// SpeedLevel / JumpLevel are the player's current effect levels.
	SpeedLevel, JumpLevel int
	// PushH / PushUp are what pushes the server gave allow
	// (Velocity.Allowance).
	PushH, PushUp float64
}

// Judge is the speed and flight check of a move that didn't phase. It
// returns the violated check and its evidence, or "".
func (m *MoveState) Judge(mv Motion) (check, detail string) {
	speedLvl := Held(mv.SpeedLevel, &m.SpeedLevel, &m.SpeedHold, effectHold)
	jumpLvl := Held(mv.JumpLevel, &m.JumpLevel, &m.JumpHold, effectHold)
	from, to := mv.From, mv.To

	// Horizontal.
	h := math.Hypot(to.X-from.X, to.Z-from.Z)
	limit := maxHorizontal*(1+speedPerLevel*float64(speedLvl)) + mv.PushH
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
	if mv.Ground || !m.HasGround || m.BounceLeft > 0 || mv.Exempt {
		m.Landed(to.Y)
		return "", ""
	}
	m.AirMoves++
	rise := to.Y - m.GroundY
	apex := Apex(JumpVelocity(jumpLvl)) + Apex(mv.PushUp) + flyMargin
	if rise > apex {
		return "movement.fly", fmt.Sprintf("%.2f above the last ground, limit %.2f", rise, apex)
	}
	dy := to.Y - from.Y
	stalled := (m.AirMoves > hoverAfter && dy > -hoverDY) || (m.AirMoves > glideAfter && dy > -glideDY)
	if stalled && mv.PushUp == 0 {
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
