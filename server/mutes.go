package server

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MuteSet tracks players whose chat is suppressed and until when. Expiry
// is lazy: lookups treat a past-expiry entry as not-muted (and the entry
// is left in the map until the next Mute/Unmute touches it). Concurrent-
// safe via RWMutex; Has is the hot path for the chat handler.
type MuteSet struct {
	mu    sync.RWMutex
	mutes map[string]time.Time // key = normalizeOpName(name); value = expiry
}

func NewMuteSet() *MuteSet {
	return &MuteSet{mutes: make(map[string]time.Time)}
}

// MutedUntil returns (expiry, true) if the player is currently muted and
// the mute hasn't expired. Otherwise (zero, false).
func (m *MuteSet) MutedUntil(name string) (time.Time, bool) {
	if m == nil {
		return time.Time{}, false
	}
	key := normalizeOpName(name)
	m.mu.RLock()
	until, ok := m.mutes[key]
	m.mu.RUnlock()
	if !ok || time.Now().After(until) {
		return time.Time{}, false
	}
	return until, true
}

// Mute adds (or overwrites) a mute. until is the moment the mute lifts —
// passing a time in the past is the same as Unmute.
func (m *MuteSet) Mute(name string, until time.Time) {
	m.mu.Lock()
	m.mutes[normalizeOpName(name)] = until
	m.mu.Unlock()
}

// Unmute removes any active mute. No-op if absent.
func (m *MuteSet) Unmute(name string) {
	m.mu.Lock()
	delete(m.mutes, normalizeOpName(name))
	m.mu.Unlock()
}

// ParseShortDuration accepts compact human strings like "30s", "5m", "2h",
// "7d", "1w" and returns the equivalent time.Duration. Units beyond
// time.ParseDuration's built-ins (d, w) are converted to hours so the
// result still survives time.Now().Add safely.
func ParseShortDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	// Last char is the unit.
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("bad number %q: %w", numStr, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative duration not allowed: %s", s)
	}
	var mult time.Duration
	switch unit {
	case 's':
		mult = time.Second
	case 'm':
		mult = time.Minute
	case 'h':
		mult = time.Hour
	case 'd':
		mult = 24 * time.Hour
	case 'w':
		mult = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("unknown unit %q (use s/m/h/d/w)", string(unit))
	}
	return time.Duration(n) * mult, nil
}
