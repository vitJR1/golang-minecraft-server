package anticheat

import (
	"math"
	"sync"
)

// velocity.go is the budget of motion the server itself gave a player —
// melee knockback, Punch arrows, explosion pushes (fireball jumps). A move
// that a vanilla client could only make with that push must not be flagged.
//
// The client applies a push when it processes the packet, which the server
// learns from the Pong to the Ping sent right after it (Transactions). Until
// then the push may land on any move, so it counts in full; afterwards the
// horizontal part decays with air drag every move and the vertical part
// lasts until the player is back on the ground.

const (
	// AirDrag is vanilla's horizontal velocity multiplier per airborne tick.
	AirDrag = 0.91
	// impulseFloor drops a confirmed push whose horizontal share fell below
	// it and whose vertical share has been used up.
	impulseFloor = 0.01
	// impulseMaxMoves drops a confirmed push after this many moves in any
	// case (5 s).
	impulseMaxMoves = 100
)

// Impulse is one push the server sent.
type Impulse struct {
	H, Up     float64 // horizontal speed, upward speed (blocks/tick)
	confirmed bool
	moves     int  // moves since confirmed
	landed    bool // touched the ground after confirmation
}

// Velocity tracks the pushes of one player. Pushes are added by whichever
// goroutine dealt them and consumed by the player's read loop.
type Velocity struct {
	mu   sync.Mutex
	list []*Impulse
}

// Add records a push of (vx, vy, vz) blocks/tick and returns it, for the
// caller to Confirm when the client's Pong arrives.
func (v *Velocity) Add(vx, vy, vz float64) *Impulse {
	im := &Impulse{H: math.Hypot(vx, vz), Up: math.Max(0, vy)}
	v.mu.Lock()
	v.list = append(v.list, im)
	v.mu.Unlock()
	return im
}

// Confirm marks im as applied by the client.
func (v *Velocity) Confirm(im *Impulse) {
	v.mu.Lock()
	im.confirmed = true
	v.mu.Unlock()
}

// Allowance is the extra horizontal distance this move may cover and the
// largest upward launch speed the player may be riding.
func (v *Velocity) Allowance() (h, up float64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, im := range v.list {
		if !im.confirmed {
			h += im.H
			up += im.Up
			continue
		}
		h += im.H * math.Pow(AirDrag, float64(im.moves))
		if !im.landed {
			up += im.Up
		}
	}
	return h, up
}

// Active reports whether any push is still in play.
func (v *Velocity) Active() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.list) > 0
}

// Moved advances confirmed pushes by one move; onGround marks their
// vertical share spent (a launch ends at the landing). Spent pushes go.
func (v *Velocity) Moved(onGround bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	keep := v.list[:0]
	for _, im := range v.list {
		if im.confirmed {
			im.moves++
			if onGround && im.moves > 1 {
				im.landed = true
			}
			spent := im.landed && im.H*math.Pow(AirDrag, float64(im.moves)) < impulseFloor
			if spent || im.moves > impulseMaxMoves {
				continue
			}
		}
		keep = append(keep, im)
	}
	v.list = keep
}

// Apex is how high a body launched upward at vy rises under vanilla
// gravity (−0.08/tick, ×0.98 drag).
func Apex(vy float64) float64 {
	h := 0.0
	for i := 0; vy > 0 && i < 200; i++ {
		h += vy
		vy = (vy - 0.08) * 0.98
	}
	return h
}

// JumpVelocity is the upward speed of a jump with Jump Boost level lvl.
func JumpVelocity(lvl int) float64 { return 0.42 + 0.1*float64(lvl) }
