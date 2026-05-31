package server

import (
	"testing"
	"time"
)

func TestParseShortDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30s", 30 * time.Second},
		{"5m", 5 * time.Minute},
		{"2h", 2 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		{"0s", 0},
	}
	for _, c := range cases {
		got, err := ParseShortDuration(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseShortDurationRejects(t *testing.T) {
	bad := []string{"", "abc", "5x", "-1m", "1.5d", "10"}
	for _, s := range bad {
		if _, err := ParseShortDuration(s); err == nil {
			t.Errorf("%q: expected error", s)
		}
	}
}

func TestMuteSetMuteAndExpire(t *testing.T) {
	m := NewMuteSet()
	if _, muted := m.MutedUntil("Bob"); muted {
		t.Error("Bob should not be muted initially")
	}
	m.Mute("Bob", time.Now().Add(time.Minute))
	if _, muted := m.MutedUntil("Bob"); !muted {
		t.Error("Bob should be muted after Mute")
	}
	// Case-insensitive lookup.
	if _, muted := m.MutedUntil("bob"); !muted {
		t.Error("lookup should be case-insensitive")
	}
	// Expired mute is treated as unmuted.
	m.Mute("Carol", time.Now().Add(-time.Minute))
	if _, muted := m.MutedUntil("Carol"); muted {
		t.Error("expired mute should not register as muted")
	}
}

func TestMuteSetUnmute(t *testing.T) {
	m := NewMuteSet()
	m.Mute("Eve", time.Now().Add(time.Hour))
	m.Unmute("Eve")
	if _, muted := m.MutedUntil("Eve"); muted {
		t.Error("Eve should be unmuted")
	}
}
