package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestIPGuardOwnLoginDoesNotResetGuesses: guessing at a victim's password,
// then logging into one's own account from the same IP, used to reset the
// IP's counter — an unbounded brute force at ~3 guesses a cycle. Only the
// account that logged in is cleared now.
func TestIPGuardOwnLoginDoesNotResetGuesses(t *testing.T) {
	g, _ := guardAt(t)
	const ip = "6.6.6.6"
	for cycle := 0; cycle < 3; cycle++ {
		banned := g.RecordFail(ip, "victim", 3, time.Hour)
		if banned != (cycle == 2) {
			t.Fatalf("cycle %d: banned=%v", cycle, banned)
		}
		g.ClearFails(ip, "attacker") // the attacker's own /login
	}
	if _, ok := g.Allowed(ip); ok {
		t.Error("IP should be banned")
	}
}

// TestIPGuardSprayBansIP: guesses spread over many accounts from one IP
// (each below the per-account limit) still ban it.
func TestIPGuardSprayBansIP(t *testing.T) {
	g, _ := guardAt(t)
	banned := false
	for i := 0; i < 3*ipSprayFactor && !banned; i++ {
		banned = g.RecordFail("7.7.7.7", fmt.Sprintf("acc%d", i), 3, time.Hour)
	}
	if !banned {
		t.Fatal("spraying one guess per account must ban the IP")
	}
}

// TestIPGuardNameWait: a name guessed at from many IPs (each below the
// per-IP limit) is throttled with a growing delay; its owner's successful
// login lifts it.
func TestIPGuardNameWait(t *testing.T) {
	g, now := guardAt(t)
	fail := func(from, to int) {
		for i := from; i < to; i++ {
			g.RecordFail(fmt.Sprintf("10.0.%d.%d", i/200, i%200), "target", 3, time.Hour)
		}
	}
	for i := 0; i < 3; i++ {
		if g.NameWait("Target", 3, time.Hour) != 0 {
			t.Fatalf("no wait below the limit (fail %d)", i)
		}
		fail(i, i+1)
	}
	if w := g.NameWait("target", 3, time.Hour); w != time.Second {
		t.Fatalf("wait after 3 fails: %v, want 1s", w)
	}
	fail(3, 6)
	if w := g.NameWait("target", 3, time.Hour); w != 8*time.Second {
		t.Fatalf("wait after 6 fails: %v, want 8s", w)
	}
	fail(6, 40)
	if w := g.NameWait("target", 3, time.Hour); w != time.Hour {
		t.Fatalf("the wait is capped at the ban duration: %v", w)
	}
	*now = now.Add(time.Hour)
	if w := g.NameWait("target", 3, time.Hour); w != 0 {
		t.Fatalf("wait over: %v", w)
	}
	fail(40, 41)
	if w := g.NameWait("target", 3, time.Hour); w != 0 {
		t.Fatalf("counter expired after the window, one new fail: %v", w)
	}
	fail(41, 44)
	if g.NameWait("target", 3, time.Hour) == 0 {
		t.Fatal("throttled again")
	}
	g.ClearFails("10.9.9.9", "target")
	if g.NameWait("target", 3, time.Hour) != 0 {
		t.Error("ClearFails lifts the name's wait")
	}
}

// TestIPGuardConcurrentSavesKeepEveryBan: bans written from several
// goroutines at once (two /banip, a brute-force ban) must all reach the
// file — saves used to share one temp file and could rename an older
// snapshot over a newer one.
func TestIPGuardConcurrentSavesKeepEveryBan(t *testing.T) {
	g, _ := guardAt(t)
	const n = 40
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Ban(fmt.Sprintf("10.0.0.%d", i), time.Hour)
		}()
	}
	wg.Wait()

	back := NewIPGuard(g.Path())
	back.now = g.now
	if err := back.Load(); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if _, ok := back.Allowed(fmt.Sprintf("10.0.0.%d", i)); ok {
			t.Errorf("ban of 10.0.0.%d lost on disk", i)
		}
	}
}

// TestIPGuardIPv6ByPrefix: a literal IPv6 /banip argument and a saved full
// IPv6 address are keyed by their /64, like connections.
func TestIPGuardIPv6ByPrefix(t *testing.T) {
	g, _ := guardAt(t)
	key := g.Resolve("2001:db8:1:2::77")
	if key != "2001:db8:1:2::/64" {
		t.Fatalf("Resolve keyed %q", key)
	}
	g.Ban(key, time.Hour)
	if _, ok := g.Allowed("2001:db8:1:2::/64"); ok {
		t.Error("the /64 should be banned")
	}
}
