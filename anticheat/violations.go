package anticheat

import (
	"sync"
	"time"
)

const (
	// DecayPerSecond is how fast a check's violation level drains while the
	// player stays clean: a stray false positive fades in seconds, a cheat
	// that fires every tick climbs regardless.
	DecayPerSecond = 0.1

	// recentViolations is how many latest flags are kept verbatim — the
	// "reasons" a punishment record cites.
	recentViolations = 32
)

// Violation is one flag raised by a check.
type Violation struct {
	Check  string    `json:"check"`
	Detail string    `json:"detail"`
	At     time.Time `json:"at"`
	VL     float64   `json:"vl"` // the check's level after this flag
}

// Violations accumulates per-check violation levels with linear decay.
type Violations struct {
	mu     sync.Mutex
	levels map[string]level
	recent []Violation // oldest first, at most recentViolations
}

type level struct {
	vl      float64
	updated time.Time
}

// decayed returns l's level at now.
func (l level) decayed(now time.Time) float64 {
	return max(0, l.vl-now.Sub(l.updated).Seconds()*DecayPerSecond)
}

// Flag adds amount to check's level (after decay) and records the detail.
// Returns the new level.
func (v *Violations) Flag(check string, amount float64, detail string, now time.Time) float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.levels == nil {
		v.levels = make(map[string]level)
	}
	vl := v.levels[check].decayed(now) + amount
	v.levels[check] = level{vl: vl, updated: now}
	if len(v.recent) == recentViolations {
		v.recent = v.recent[1:]
	}
	v.recent = append(v.recent, Violation{Check: check, Detail: detail, At: now, VL: vl})
	return vl
}

// Level returns check's current (decayed) violation level.
func (v *Violations) Level(check string, now time.Time) float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.levels[check].decayed(now)
}

// Recent returns a copy of the latest flags, oldest first.
func (v *Violations) Recent() []Violation {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]Violation(nil), v.recent...)
}
