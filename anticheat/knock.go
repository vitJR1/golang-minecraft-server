package anticheat

import (
	"fmt"
	"math"
)

// knock.go: anti-knockback (anticheat.md #13) — following a push the client
// confirmed over its next moves.

const (
	// knockMoves: moves after a confirmed push before judging it.
	knockMoves = 5
	// knockMinH / knockMinUp: pushes smaller than these aren't judged.
	knockMinH  = 0.2
	knockMinUp = 0.25
	// knockShare: the least share of the push a client that applied it
	// shows — even walking straight back into it.
	knockShareH  = 0.3
	knockShareUp = 0.2
)

// StartKnock begins verifying im, confirmed by the client while it stood at
// start; false (and nothing to verify) for a push too small to judge.
func StartKnock(im *Impulse, start Vec) (KnockCheck, bool) {
	if im.H < knockMinH && im.Up < knockMinUp {
		return KnockCheck{}, false
	}
	return KnockCheck{Active: true, DirX: im.DirX, DirZ: im.DirZ, H: im.H, Up: im.Up, Start: start}, true
}

// Follow takes the next move (to) and, once enough moves are in, returns
// evidence of anti-knockback, or "". A wall in the push's way or a ceiling
// over the head (blocked) excuses a short move; so does a fluid or cobweb
// (exempt), which ends the check.
func (k *KnockCheck) Follow(to Vec, exempt bool, blocked func(feet Vec) bool) string {
	if !k.Active {
		return ""
	}
	if exempt {
		k.Active = false
		return ""
	}
	k.Moves++
	k.BestH = math.Max(k.BestH, (to.X-k.Start.X)*k.DirX+(to.Z-k.Start.Z)*k.DirZ)
	k.BestUp = math.Max(k.BestUp, to.Y-k.Start.Y)
	if k.Moves < knockMoves {
		return ""
	}
	k.Active = false
	var weak []string
	if k.H >= knockMinH && k.BestH < knockShareH*k.H {
		ahead := Vec{X: k.Start.X + k.DirX*0.6, Y: k.Start.Y, Z: k.Start.Z + k.DirZ*0.6}
		if !blocked(ahead) {
			weak = append(weak, fmt.Sprintf("moved %.2f along a %.2f push", k.BestH, k.H))
		}
	}
	if k.Up >= knockMinUp && k.BestUp < knockShareUp*Apex(k.Up) {
		above := Vec{X: k.Start.X, Y: k.Start.Y + 0.6, Z: k.Start.Z}
		if !blocked(above) {
			weak = append(weak, fmt.Sprintf("rose %.2f on a %.2f lift", k.BestUp, k.Up))
		}
	}
	if len(weak) == 0 {
		return ""
	}
	return fmt.Sprint(weak)
}
