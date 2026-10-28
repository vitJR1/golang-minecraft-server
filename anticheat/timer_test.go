package anticheat

import (
	"testing"
	"time"
)

// feed sends n movement packets every interval starting at t0; it returns
// how many were flagged and the time after the last one.
func feed(tm *Timer, t0 time.Time, n int, interval time.Duration) (int, time.Time) {
	flags := 0
	now := t0
	for i := 0; i < n; i++ {
		if _, f := tm.Move(now); f {
			flags++
		}
		now = now.Add(interval)
	}
	return flags, now
}

func TestTimerVanillaPaceNeverFlags(t *testing.T) {
	var tm Timer
	if flags, _ := feed(&tm, time.Unix(0, 0), 2000, TickDuration); flags != 0 {
		t.Fatalf("20 packets/s flagged %d times", flags)
	}
}

func TestTimerFastClockFlags(t *testing.T) {
	var tm Timer
	// 1.25× game speed for 10 s: 250 packets in 10 s.
	flags, _ := feed(&tm, time.Unix(0, 0), 250, 40*time.Millisecond)
	if flags < 3 {
		t.Fatalf("1.25× timer flagged %d times in 10 s", flags)
	}
}

func TestTimerLagBurstDoesNotFlag(t *testing.T) {
	var tm Timer
	_, now := feed(&tm, time.Unix(0, 0), 100, TickDuration)
	// The network stalls for 400 ms, then the 8 packets sent meanwhile
	// arrive together, then the normal pace resumes.
	now = now.Add(400 * time.Millisecond)
	burst, now := feed(&tm, now, 8, 0)
	rest, _ := feed(&tm, now, 100, TickDuration)
	if burst+rest != 0 {
		t.Fatalf("a lag burst flagged %d times", burst+rest)
	}
}

func TestTimerIdleBankIsCapped(t *testing.T) {
	var tm Timer
	_, now := feed(&tm, time.Unix(0, 0), 1, TickDuration)
	now = now.Add(time.Hour) // AFK
	// Afterwards a 2× timer: the hour of idling can't pay for it.
	flags, _ := feed(&tm, now, 200, 25*time.Millisecond)
	if flags == 0 {
		t.Fatal("an hour of AFK banked unlimited timer")
	}
}

func TestTimerCreditForTeleport(t *testing.T) {
	var tm Timer
	now := time.Unix(0, 0)
	_, now = feed(&tm, now, 10, TickDuration)
	// Every tick a teleport: the regular packet plus the extra one the
	// client sends with its confirm, both in the same tick.
	flags := 0
	for i := 0; i < 40; i++ {
		tm.Credit()
		f, n := feed(&tm, now, 2, 0)
		flags += f
		now = n.Add(TickDuration)
	}
	if flags != 0 {
		t.Fatalf("credited teleport packets flagged %d times", flags)
	}
}
