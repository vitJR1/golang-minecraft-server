// Package tickloop is an instance's game clock: a 20 Hz loop running the
// registered handlers in order, the tick counter, panic isolation and the
// tick timings /stats reports. The zero Loop is ready to use.
package tickloop

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Rate is the tick frequency in Hz. Matches vanilla.
const Rate = 20

// Interval is the wall-clock duration between ticks.
var Interval = time.Second / Rate

// Handler fires once per tick. tick is the monotonically-increasing counter
// since the loop started. Handlers should be fast — if any handler takes
// longer than Interval, the next tick is dropped (the ticker doesn't queue),
// so a slow handler stretches game time visibly.
type Handler func(tick uint64)

// Loop is one clock. Label names it in panic logs (the instance id).
type Loop struct {
	Label string

	count    atomic.Uint64
	mu       sync.RWMutex
	handlers []Handler

	initOnce sync.Once
	stop     chan struct{} // closed by Stop
	stopOnce sync.Once

	// Duration of the most recent tick and the slowest one so far, in
	// nanoseconds. Written by the loop, read by metrics.
	lastNanos atomic.Int64
	maxNanos  atomic.Int64
}

func (l *Loop) stopCh() chan struct{} {
	l.initOnce.Do(func() { l.stop = make(chan struct{}) })
	return l.stop
}

// Handle registers h to run on every tick, after the ones registered
// before it. Concurrent-safe; there is no unsubscribe.
func (l *Loop) Handle(h Handler) {
	l.mu.Lock()
	l.handlers = append(l.handlers, h)
	l.mu.Unlock()
}

// Count is the current tick (0 before the first one).
func (l *Loop) Count() uint64 { return l.count.Load() }

// Advance moves the counter n ticks on without running anything and
// returns the new count (tests, a bot test's own stepping).
func (l *Loop) Advance(n uint64) uint64 { return l.count.Add(n) }

// Set puts the counter at n (tests).
func (l *Loop) Set(n uint64) { l.count.Store(n) }

// Last is how long the most recent tick took; Max the slowest one so far.
func (l *Loop) Last() time.Duration { return time.Duration(l.lastNanos.Load()) }
func (l *Loop) Max() time.Duration  { return time.Duration(l.maxNanos.Load()) }

// Run ticks every Interval until Stop. Call it on its own goroutine.
func (l *Loop) Run() {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	stop := l.stopCh()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			l.Step()
		}
	}
}

// Step runs one tick now: bumps the counter and runs every handler, each
// under panic recovery so one buggy game can't freeze the clock.
func (l *Loop) Step() {
	tick := l.count.Add(1)
	start := time.Now()

	// Snapshot the handler list so a handler can Handle a new subscriber
	// without deadlocking, and so we don't hold the lock during user code.
	l.mu.RLock()
	handlers := make([]Handler, len(l.handlers))
	copy(handlers, l.handlers)
	l.mu.RUnlock()

	for _, h := range handlers {
		l.safe(h, tick)
	}

	elapsed := int64(time.Since(start))
	l.lastNanos.Store(elapsed)
	if elapsed > l.maxNanos.Load() {
		// Plain store, not CAS: only the loop writes, so a lost race
		// can't happen.
		l.maxNanos.Store(elapsed)
	}
}

func (l *Loop) safe(h Handler, tick uint64) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("tick handler panic",
				"instance", l.Label, "tick", tick, "panic", fmt.Sprint(r))
		}
	}()
	h(tick)
}

// Stop ends Run. The first call runs before (if non-nil) while the loop is
// still going, then stops it; later calls do nothing.
func (l *Loop) Stop(before func()) {
	l.stopOnce.Do(func() {
		if before != nil {
			before()
		}
		close(l.stopCh())
	})
}
