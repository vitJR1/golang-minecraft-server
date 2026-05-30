package server

import (
	"minecraft-server/world"
	"sync/atomic"
	"testing"
	"time"
)

// newTestInstance creates an instance whose tick loop is cleaned up after
// the test, so no leaked goroutines.
func newTestInstance(t *testing.T) *Instance {
	t.Helper()
	s := New()
	t.Cleanup(s.Hub.Stop)
	i := NewInstance("test-"+t.Name(), s, world.NewMemoryWorld())
	t.Cleanup(i.Stop)
	return i
}

func TestTickAdvances(t *testing.T) {
	i := newTestInstance(t)

	// Wait long enough for several ticks; 150ms = ~3 ticks at 20Hz.
	time.Sleep(150 * time.Millisecond)
	got := i.Tick()
	if got < 2 || got > 5 {
		// Wide tolerance — schedulers under load vary. We just want to
		// confirm the loop is actually running at roughly the right rate.
		t.Errorf("Tick: got %d after 150ms, expected ~3", got)
	}
}

func TestOnTickFires(t *testing.T) {
	i := newTestInstance(t)

	var calls atomic.Int64
	var lastTick atomic.Uint64
	i.OnTick(func(tick uint64) {
		calls.Add(1)
		lastTick.Store(tick)
	})

	time.Sleep(150 * time.Millisecond)
	if calls.Load() < 2 {
		t.Errorf("handler called %d times, want >= 2", calls.Load())
	}
	if lastTick.Load() == 0 {
		t.Error("lastTick is 0 — handler never received a non-zero tick")
	}
}

func TestStopHaltsTicks(t *testing.T) {
	i := newTestInstance(t)

	time.Sleep(80 * time.Millisecond)
	before := i.Tick()
	i.Stop()

	// After Stop, no more increments. Wait beyond a tick interval to
	// give a misbehaving loop a chance to bump the counter.
	time.Sleep(150 * time.Millisecond)
	after := i.Tick()
	if after != before {
		t.Errorf("Tick advanced after Stop: %d → %d", before, after)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	i := newTestInstance(t)
	i.Stop()
	i.Stop() // would panic on close(closed channel) without sync.Once
}

func TestPanickingHandlerDoesNotKillLoop(t *testing.T) {
	i := newTestInstance(t)

	var goodCalls atomic.Int64
	i.OnTick(func(tick uint64) {
		panic("boom")
	})
	i.OnTick(func(tick uint64) {
		goodCalls.Add(1)
	})

	time.Sleep(150 * time.Millisecond)
	if goodCalls.Load() < 2 {
		t.Errorf("good handler called %d times, want >= 2 — bad handler killed loop?",
			goodCalls.Load())
	}
}

func TestSafeHookCatchesPanic(t *testing.T) {
	i := newTestInstance(t)
	// Just verifies safeHook doesn't crash the test on a panicking callback.
	safeHook(i, "test", func() { panic("boom") })
}

func TestMultipleHandlers(t *testing.T) {
	i := newTestInstance(t)

	var a, b, c atomic.Int64
	i.OnTick(func(uint64) { a.Add(1) })
	i.OnTick(func(uint64) { b.Add(1) })
	i.OnTick(func(uint64) { c.Add(1) })

	time.Sleep(100 * time.Millisecond)
	if a.Load() == 0 || b.Load() == 0 || c.Load() == 0 {
		t.Errorf("handler counts: a=%d b=%d c=%d — all should be >0",
			a.Load(), b.Load(), c.Load())
	}
}
