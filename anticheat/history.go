package anticheat

import "sync"

// HistorySize is how many ticks of positions are kept: 64 ticks = 3.2 s,
// enough to rewind past any playable ping.
const HistorySize = 64

// Sample is a player's server-accepted position at the end of one tick.
type Sample struct {
	Tick     uint64
	X, Y, Z  float64
	Sneaking bool
}

// History is a ring of the last HistorySize per-tick samples. Written by the
// instance tick, read by checks on other players' read loops.
type History struct {
	mu   sync.Mutex
	buf  [HistorySize]Sample
	head int // index of the next write
	n    int
}

// Record appends the sample for s.Tick. A second sample for the same tick
// replaces the first.
func (h *History) Record(s Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n > 0 {
		last := (h.head - 1 + HistorySize) % HistorySize
		if h.buf[last].Tick == s.Tick {
			h.buf[last] = s
			return
		}
	}
	h.buf[h.head] = s
	h.head = (h.head + 1) % HistorySize
	if h.n < HistorySize {
		h.n++
	}
}

// Since returns the samples with Tick >= tick, oldest first. A check that
// knows the attacker had seen the world up to tick T asks for Since(T) and
// accepts a hit that fits any of them.
func (h *History) Since(tick uint64) []Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Sample
	for i := 0; i < h.n; i++ {
		s := h.buf[(h.head-h.n+i+HistorySize)%HistorySize]
		if s.Tick >= tick {
			out = append(out, s)
		}
	}
	return out
}

// Latest returns the newest sample.
func (h *History) Latest() (Sample, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n == 0 {
		return Sample{}, false
	}
	return h.buf[(h.head-1+HistorySize)%HistorySize], true
}

// Reset drops every sample.
func (h *History) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.head, h.n = 0, 0
}
