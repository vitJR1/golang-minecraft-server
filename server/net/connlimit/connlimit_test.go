package connlimit

import (
	"minecraft-server/cfg"
	"testing"
	"time"
)

// withConnLimits temporarily overrides the flood-limit config for one test.
func withConnLimits(t *testing.T, total, perIP, rate int) {
	t.Helper()
	prevTotal, prevPerIP, prevRate := cfg.MaxConns, cfg.MaxConnsPerIP, cfg.ConnRatePerIP
	cfg.MaxConns, cfg.MaxConnsPerIP, cfg.ConnRatePerIP = total, perIP, rate
	t.Cleanup(func() {
		cfg.MaxConns, cfg.MaxConnsPerIP, cfg.ConnRatePerIP = prevTotal, prevPerIP, prevRate
	})
}

// fakeClock returns a limiter clock the test can advance manually.
func fakeClock(l *Limiter) *time.Time {
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	return &now
}

func TestConnLimiterPerIPConcurrent(t *testing.T) {
	withConnLimits(t, 0, 2, 0)
	l := New()
	fakeClock(l)

	r1, _ := l.Acquire("1.2.3.4")
	r2, _ := l.Acquire("1.2.3.4")
	if r1 == nil || r2 == nil {
		t.Fatal("first two connections should be admitted")
	}
	if r3, reason := l.Acquire("1.2.3.4"); r3 != nil {
		t.Fatal("third concurrent connection should be rejected")
	} else if reason != "per-IP connection limit" {
		t.Errorf("reason: got %q", reason)
	}
	// A different IP is unaffected.
	if r, _ := l.Acquire("5.6.7.8"); r == nil {
		t.Fatal("other IP should be admitted")
	}
	// Releasing frees the slot.
	r1()
	if r, _ := l.Acquire("1.2.3.4"); r == nil {
		t.Fatal("after release the slot should be free")
	}
}

func TestConnLimiterRateWindow(t *testing.T) {
	withConnLimits(t, 0, 0, 3)
	l := New()
	now := fakeClock(l)

	for i := range 3 {
		r, _ := l.Acquire("1.2.3.4")
		if r == nil {
			t.Fatalf("connect %d should be admitted", i)
		}
		r() // release immediately — rate counts connects, not concurrency
	}
	if r, reason := l.Acquire("1.2.3.4"); r != nil {
		t.Fatal("4th connect inside the window should be rejected")
	} else if reason != "per-IP connection rate" {
		t.Errorf("reason: got %q", reason)
	}

	// Past the window the counter resets.
	*now = now.Add(connRateWindow)
	if r, _ := l.Acquire("1.2.3.4"); r == nil {
		t.Fatal("connect after window expiry should be admitted")
	}
}

func TestConnLimiterTotalCap(t *testing.T) {
	withConnLimits(t, 2, 0, 0)
	l := New()
	fakeClock(l)

	r1, _ := l.Acquire("1.1.1.1")
	r2, _ := l.Acquire("2.2.2.2")
	if r1 == nil || r2 == nil {
		t.Fatal("connections under the cap should be admitted")
	}
	if r, reason := l.Acquire("3.3.3.3"); r != nil {
		t.Fatal("connection over the total cap should be rejected")
	} else if reason != "server connection limit" {
		t.Errorf("reason: got %q", reason)
	}
	r2()
	if r, _ := l.Acquire("3.3.3.3"); r == nil {
		t.Fatal("after release the total slot should be free")
	}
}

func TestConnLimiterSweep(t *testing.T) {
	withConnLimits(t, 0, 5, 5)
	l := New()
	now := fakeClock(l)

	release, _ := l.Acquire("1.2.3.4")
	release()
	r2, _ := l.Acquire("5.6.7.8") // stays active — must survive the sweep

	*now = now.Add(connSweepInterval + connRateWindow)
	l.Acquire("9.9.9.9") // triggers the sweep

	l.mu.Lock()
	_, idleStillThere := l.perIP["1.2.3.4"]
	_, activeKept := l.perIP["5.6.7.8"]
	l.mu.Unlock()
	if idleStillThere {
		t.Error("idle expired entry should have been swept")
	}
	if !activeKept {
		t.Error("entry with active connection must survive the sweep")
	}
	r2()
}
