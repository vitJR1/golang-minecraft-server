package anticheat

import (
	"time"
	"unicode/utf8"
)

// Packet budget: a vanilla client sends ~20 movement + ~20 Pong packets a
// second plus bursts of clicks; a few hundred per second is already absurd.
// The bucket refills at PacketRate and holds PacketBurst, so a short burst
// (creative inventory shuffles, drag-clicks) passes while a flood doesn't.
const (
	PacketRate  = 400.0
	PacketBurst = 800.0
)

// RateLimiter is a token bucket over wall-clock time. The zero value uses
// PacketRate / PacketBurst and starts full. Only the connection's read loop
// calls it, so it has no lock.
type RateLimiter struct {
	Rate, Burst float64 // 0 = the Packet* defaults

	tokens  float64
	last    time.Time
	started bool
}

// Allow takes one token, reporting false when the bucket is empty.
func (r *RateLimiter) Allow(now time.Time) bool {
	rate, burst := r.Rate, r.Burst
	if rate == 0 {
		rate = PacketRate
	}
	if burst == 0 {
		burst = PacketBurst
	}
	if !r.started {
		r.tokens, r.last, r.started = burst, now, true
	}
	if dt := now.Sub(r.last).Seconds(); dt > 0 {
		r.tokens = min(burst, r.tokens+dt*rate)
		r.last = now
	}
	if r.tokens < 1 {
		return false
	}
	r.tokens--
	return true
}

// ValidHotbarSlot: Set Held Item carries a hotbar index 0..8.
func ValidHotbarSlot(slot int) bool {
	return slot >= 0 && slot <= 8
}

// ValidPitch: the vanilla client clamps its pitch to [-90, 90].
func ValidPitch(pitch float32) bool {
	return pitch >= -90 && pitch <= 90
}

// Chat budget, vanilla's chatSpamTickCount: +20 per message, −1 per tick,
// kick above 200 — i.e. a burst of 10, then one message a second.
const (
	ChatRate  = 1.0
	ChatBurst = 10.0
)

// Drop budget: items thrown per second (Q, ctrl+Q, window throws). Dumping
// a whole inventory stack by stack still fits; an item-entity flood doesn't.
const (
	DropRate  = 20.0
	DropBurst = 40.0
)

// MaxChatLength is vanilla's limit on a chat message or command (256
// characters).
const MaxChatLength = 256

// ValidChat reports whether text could come from a vanilla chat box: at
// most MaxChatLength characters, none of them '§' (formatting codes),
// control characters or DEL — SharedConstants.isAllowedChatCharacter. A
// '§' or newline would let a player forge coloured or multi-line system
// text.
func ValidChat(text string) bool {
	if utf8.RuneCountInString(text) > MaxChatLength || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if r == '§' || r < ' ' || r == 0x7f {
			return false
		}
	}
	return true
}
