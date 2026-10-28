// Package anticheat holds the server-side bookkeeping cheat checks are built
// on: what the server has told the client (teleports, transactions), where
// every player was on recent ticks, the client's declared input state, and
// per-check violation levels. It knows nothing about the wire or the server
// package — server/ feeds it events and decides how to react (see
// anticheat.md in the repo root).
package anticheat

import (
	"sync/atomic"
	"time"
)

// State is one connection's anticheat state. The zero value is ready to use,
// so it can live by value inside the connection struct. Each component
// guards itself: teleports and transactions are begun from whichever
// goroutine moves the player or runs the tick, and confirmed from the
// connection's read loop.
type State struct {
	// Teleports are the server-initiated position syncs the client has not
	// confirmed yet. While any is pending, the client's movement packets
	// describe a position the server already overrode and are ignored.
	Teleports Teleports

	// Transactions numbers server events with Ping/Pong round-trips, so a
	// check can tell which server tick the client had seen when it acted.
	Transactions Transactions

	// History is the player's accepted position on recent ticks: what other
	// clients were shown, used to compensate reach for the attacker's ping.
	History History

	// Violations accumulates per-check violation levels.
	Violations Violations

	// Packets rate-limits everything the client sends in play state.
	Packets RateLimiter
	// Timer balances movement packets against real time (timer.go).
	Timer Timer
	// Move is the server's view of the player's movement (read loop only).
	Move MoveState
	// Chat rate-limits chat messages and commands (AllowChat); Drops the
	// items the player throws (AllowDrop).
	Chat  RateLimiter
	Drops RateLimiter

	// Sprinting / Sneaking mirror the client's Player Command input. They are
	// the client's claim, not verified physics.
	Sprinting atomic.Bool
	Sneaking  atomic.Bool

	// Detected is set by the first detection of the session: one detection
	// moves the ladder one step, however many checks fire before the kick.
	Detected atomic.Bool
}

// MoveState is what the server worked out about the player's movement from
// the accepted positions, instead of trusting the packet's flags.
type MoveState struct {
	// LastDY is the vertical change of the latest accepted move: below zero
	// the player is falling (a critical hit needs that).
	LastDY float64
}

// Reset forgets everything tied to the current world: positions and the
// transaction → tick mapping. Called on a move to another instance, whose
// tick counter and coordinates are unrelated. Pending teleports stay — the
// client still confirms them in order; violation levels and the packet
// budget are per-connection and survive too.
func (s *State) Reset() {
	s.History.Reset()
	s.Transactions.NewEpoch()
}

// AllowChat spends one chat message of the ChatRate/ChatBurst budget.
func (s *State) AllowChat(now time.Time) bool {
	s.Chat.Rate, s.Chat.Burst = ChatRate, ChatBurst
	return s.Chat.Allow(now)
}

// AllowDrop spends one thrown item stack of the DropRate/DropBurst budget.
func (s *State) AllowDrop(now time.Time) bool {
	s.Drops.Rate, s.Drops.Burst = DropRate, DropBurst
	return s.Drops.Allow(now)
}
