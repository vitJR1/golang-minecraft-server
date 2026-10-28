package anticheat

import (
	"math"
	"time"
)

// actions.go: budgets and geometry for the world/combat heuristics —
// fastbreak / nuker, fastplace / scaffold, killaura / autoclicker
// (anticheat.md #10–#12).

// Action budgets (token buckets, per second / burst).
const (
	// BreakRate: survival breaks resolve at most once a tick on a vanilla
	// client, instant-mined blocks included; a nuker clears dozens a tick.
	BreakRate, BreakBurst = 20.0, 5.0
	// PlaceRate: right-click held places every 4 ticks, a fast hand
	// (god-bridging) clicks ~10–12 times a second.
	PlaceRate, PlaceBurst = 12.0, 6.0
	// AttackRate: the fastest human clicking is ~20 CPS (butterfly); an
	// autoclicker / aura lands far more.
	AttackRate, AttackBurst = 20.0, 6.0
)

// AllowBreak / AllowPlace / AllowAttack spend one action of their budget.
func (s *State) AllowBreak(now time.Time) bool {
	s.Breaks.Rate, s.Breaks.Burst = BreakRate, BreakBurst
	return s.Breaks.Allow(now)
}

func (s *State) AllowPlace(now time.Time) bool {
	s.Places.Rate, s.Places.Burst = PlaceRate, PlaceBurst
	return s.Places.Allow(now)
}

func (s *State) AllowAttack(now time.Time) bool {
	s.Attacks.Rate, s.Attacks.Burst = AttackRate, AttackBurst
	return s.Attacks.Allow(now)
}

// Look is the unit view vector for a yaw / pitch in degrees (vanilla
// Entity.calculateViewVector).
func Look(yaw, pitch float32) Vec {
	y, p := float64(yaw)*math.Pi/180, float64(pitch)*math.Pi/180
	return Vec{X: -math.Sin(y) * math.Cos(p), Y: -math.Sin(p), Z: math.Cos(y) * math.Cos(p)}
}

// RayHitsBox reports whether the ray from origin along dir (unit) meets box
// within maxDist (slab test).
func RayHitsBox(origin, dir Vec, box Box, maxDist float64) bool {
	tmin, tmax := 0.0, maxDist
	axis := func(o, d, lo, hi float64) bool {
		if math.Abs(d) < 1e-12 {
			return o >= lo && o <= hi
		}
		t1, t2 := (lo-o)/d, (hi-o)/d
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		tmin, tmax = math.Max(tmin, t1), math.Min(tmax, t2)
		return tmin <= tmax
	}
	return axis(origin.X, dir.X, box.Min.X, box.Max.X) &&
		axis(origin.Y, dir.Y, box.Min.Y, box.Max.Y) &&
		axis(origin.Z, dir.Z, box.Min.Z, box.Max.Z)
}

// AngleTo is the angle in degrees between dir (unit) and the direction from
// origin to target; 0 when they coincide.
func AngleTo(origin, dir, target Vec) float64 {
	d := Dist(origin, target)
	if d < 1e-9 {
		return 0
	}
	dot := (dir.X*(target.X-origin.X) + dir.Y*(target.Y-origin.Y) + dir.Z*(target.Z-origin.Z)) / d
	return math.Acos(math.Max(-1, math.Min(1, dot))) * 180 / math.Pi
}

// FaceVisible reports whether face f (0 −Y, 1 +Y, 2 −Z, 3 +Z, 4 −X, 5 +X)
// of the block at (x, y, z) faces an eye at e, within slack: the vanilla
// client can only click a face it sees, so the eye is on the outer side of
// the face's plane.
func FaceVisible(e Vec, x, y, z, face int, slack float64) bool {
	switch face {
	case 0:
		return e.Y <= float64(y)+slack
	case 1:
		return e.Y >= float64(y+1)-slack
	case 2:
		return e.Z <= float64(z)+slack
	case 3:
		return e.Z >= float64(z+1)-slack
	case 4:
		return e.X <= float64(x)+slack
	case 5:
		return e.X >= float64(x+1)-slack
	}
	return false
}
