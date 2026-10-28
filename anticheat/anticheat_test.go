package anticheat

import (
	"errors"
	"testing"
	"time"
)

func TestTeleportsConfirmInOrder(t *testing.T) {
	var tp Teleports
	if tp.Awaiting() {
		t.Fatal("zero value awaits a teleport")
	}
	a := tp.Begin(1, 2, 3)
	b := tp.Begin(4, 5, 6)
	if a == b || a <= 0 {
		t.Fatalf("ids %d, %d: want distinct positive", a, b)
	}

	// Confirming the older one still leaves the newer pending.
	if got, ok := tp.Confirm(a); !ok || got.X != 1 {
		t.Fatalf("confirm %d: %+v %v", a, got, ok)
	}
	if !tp.Awaiting() {
		t.Fatal("newer teleport should still be pending")
	}
	if _, ok := tp.Confirm(a); ok {
		t.Fatal("a settled id confirmed twice")
	}
	if _, ok := tp.Confirm(b); !ok || tp.Awaiting() {
		t.Fatal("confirming the newest should settle everything")
	}
}

func TestTeleportsNewestSettlesOlder(t *testing.T) {
	var tp Teleports
	tp.Begin(0, 0, 0)
	last := tp.Begin(9, 9, 9)
	if _, ok := tp.Confirm(last); !ok || tp.Awaiting() {
		t.Fatal("newest confirm should settle the older one too")
	}
}

func TestTeleportsForgedID(t *testing.T) {
	var tp Teleports
	tp.Begin(0, 0, 0)
	if _, ok := tp.Confirm(12345); ok {
		t.Fatal("unknown id accepted")
	}
	if !tp.Awaiting() {
		t.Fatal("a forged confirm settled the real teleport")
	}
}

func TestTeleportsQueueBounded(t *testing.T) {
	var tp Teleports
	var last int32
	for i := 0; i < maxPendingTeleports*3; i++ {
		last = tp.Begin(0, 0, 0)
	}
	if n := len(tp.pending); n != maxPendingTeleports {
		t.Fatalf("pending = %d, want %d", n, maxPendingTeleports)
	}
	if _, ok := tp.Confirm(last); !ok {
		t.Fatal("newest teleport lost")
	}
}

func TestTransactionsConfirmTickAndRTT(t *testing.T) {
	var tx Transactions
	t0 := time.Unix(1000, 0)
	if _, ok := tx.LastConfirmedTick(); ok {
		t.Fatal("confirmed tick before any pong")
	}
	called := false
	a, _ := tx.Begin(10, t0, func() { called = true })
	b, _ := tx.Begin(11, t0.Add(50*time.Millisecond), nil)

	cb, err := tx.Confirm(a, t0.Add(80*time.Millisecond))
	if err != nil || cb == nil {
		t.Fatalf("confirm a: cb=%v err=%v", cb != nil, err)
	}
	cb()
	if !called {
		t.Fatal("callback not returned")
	}
	if tick, ok := tx.LastConfirmedTick(); !ok || tick != 10 {
		t.Fatalf("last confirmed tick = %d %v, want 10", tick, ok)
	}
	if rtt := tx.RTT(); rtt != 80*time.Millisecond {
		t.Fatalf("rtt = %v, want 80ms", rtt)
	}
	if _, err := tx.Confirm(b, t0.Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if tick, _ := tx.LastConfirmedTick(); tick != 11 {
		t.Fatalf("last confirmed tick = %d, want 11", tick)
	}
}

func TestTransactionsRejectSkipsAndForgeries(t *testing.T) {
	var tx Transactions
	now := time.Unix(0, 0)
	a, _ := tx.Begin(1, now, nil)
	b, _ := tx.Begin(2, now, nil)

	if _, err := tx.Confirm(b, now); !errors.Is(err, ErrTransactionOrder) {
		t.Fatalf("skipping %d: err = %v, want order error", a, err)
	}
	if _, err := tx.Confirm(999, now); !errors.Is(err, ErrUnknownTransaction) {
		t.Fatalf("forged id: err = %v", err)
	}
	if tx.Outstanding() != 2 {
		t.Fatal("rejected pongs must not settle anything")
	}
}

func TestTransactionsOutstandingCap(t *testing.T) {
	var tx Transactions
	for i := 0; i < MaxOutstanding; i++ {
		if _, ok := tx.Begin(uint64(i), time.Time{}, nil); !ok {
			t.Fatalf("refused at %d", i)
		}
	}
	if _, ok := tx.Begin(0, time.Time{}, nil); ok {
		t.Fatal("cap not enforced")
	}
}

func TestTransactionsEpoch(t *testing.T) {
	var tx Transactions
	now := time.Unix(0, 0)
	old, _ := tx.Begin(500, now, nil)
	tx.NewEpoch()
	// The old-instance ping is still answered in order, but its tick is
	// from another timeline.
	if _, err := tx.Confirm(old, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := tx.LastConfirmedTick(); ok {
		t.Fatal("old-epoch tick leaked into the new instance")
	}
	fresh, _ := tx.Begin(3, now, nil)
	_, _ = tx.Confirm(fresh, now)
	if tick, ok := tx.LastConfirmedTick(); !ok || tick != 3 {
		t.Fatalf("tick = %d %v, want 3", tick, ok)
	}
}

func TestHistoryRing(t *testing.T) {
	var h History
	if _, ok := h.Latest(); ok {
		t.Fatal("empty history has a latest sample")
	}
	for tick := uint64(1); tick <= HistorySize+10; tick++ {
		h.Record(Sample{Tick: tick, X: float64(tick)})
	}
	all := h.Since(0)
	if len(all) != HistorySize {
		t.Fatalf("kept %d samples, want %d", len(all), HistorySize)
	}
	if all[0].Tick != 11 || all[len(all)-1].Tick != HistorySize+10 {
		t.Fatalf("window %d..%d, want oldest first 11..%d", all[0].Tick, all[len(all)-1].Tick, HistorySize+10)
	}
	if got := h.Since(HistorySize + 8); len(got) != 3 || got[0].X != HistorySize+8 {
		t.Fatalf("since: %+v", got)
	}

	// Same tick twice keeps one sample, the latest.
	h.Record(Sample{Tick: HistorySize + 10, X: -1})
	if s, _ := h.Latest(); s.X != -1 || len(h.Since(0)) != HistorySize {
		t.Fatalf("same-tick record: latest=%+v", s)
	}

	h.Reset()
	if len(h.Since(0)) != 0 {
		t.Fatal("reset kept samples")
	}
}

func TestViolationsDecay(t *testing.T) {
	var v Violations
	t0 := time.Unix(0, 0)
	if vl := v.Flag("reach", 2, "4.1 blocks", t0); vl != 2 {
		t.Fatalf("vl = %v", vl)
	}
	// 10 s later 1.0 has drained.
	if vl := v.Flag("reach", 1, "4.3 blocks", t0.Add(10*time.Second)); vl != 2 {
		t.Fatalf("vl after decay = %v, want 2", vl)
	}
	if lvl := v.Level("reach", t0.Add(time.Hour)); lvl != 0 {
		t.Fatalf("level never drains to 0: %v", lvl)
	}
	if lvl := v.Level("fly", t0); lvl != 0 {
		t.Fatalf("unflagged check has level %v", lvl)
	}
	rec := v.Recent()
	if len(rec) != 2 || rec[1].Detail != "4.3 blocks" || rec[1].VL != 2 {
		t.Fatalf("recent = %+v", rec)
	}
}

func TestViolationsRecentBounded(t *testing.T) {
	var v Violations
	for i := 0; i < recentViolations+5; i++ {
		v.Flag("x", 1, "", time.Unix(int64(i), 0))
	}
	if n := len(v.Recent()); n != recentViolations {
		t.Fatalf("recent = %d, want %d", n, recentViolations)
	}
}

func TestRateLimiter(t *testing.T) {
	r := RateLimiter{Rate: 10, Burst: 5}
	now := time.Unix(0, 0)
	for i := 0; i < 5; i++ {
		if !r.Allow(now) {
			t.Fatalf("burst refused at %d", i)
		}
	}
	if r.Allow(now) {
		t.Fatal("bucket should be empty")
	}
	// 100 ms at 10/s = one token.
	if !r.Allow(now.Add(100 * time.Millisecond)) {
		t.Fatal("no refill")
	}
	if r.Allow(now.Add(100 * time.Millisecond)) {
		t.Fatal("refilled too much")
	}
	// A long pause refills to the burst, not beyond.
	later := now.Add(time.Hour)
	for i := 0; i < 5; i++ {
		r.Allow(later)
	}
	if r.Allow(later) {
		t.Fatal("bucket exceeded burst")
	}
}

func TestRateLimiterDefaults(t *testing.T) {
	var r RateLimiter
	now := time.Unix(0, 0)
	n := 0
	for r.Allow(now) {
		n++
	}
	if n != PacketBurst {
		t.Fatalf("zero-value burst = %d, want %v", n, PacketBurst)
	}
}

func TestValidators(t *testing.T) {
	for _, s := range []int{0, 8} {
		if !ValidHotbarSlot(s) {
			t.Errorf("slot %d rejected", s)
		}
	}
	for _, s := range []int{-1, 9, 36} {
		if ValidHotbarSlot(s) {
			t.Errorf("slot %d accepted", s)
		}
	}
	if !ValidPitch(90) || !ValidPitch(-90) || ValidPitch(90.5) || ValidPitch(-180) {
		t.Error("pitch bounds wrong")
	}
}

func TestValidChat(t *testing.T) {
	for _, ok := range []string{"hello", "привет мир 🌍", "/tp 1 2 3", ""} {
		if !ValidChat(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	long := make([]rune, MaxChatLength+1)
	for i := range long {
		long[i] = 'a'
	}
	for _, bad := range []string{"§cred", "line\nbreak", "tab\there", "nul\x00", "del\x7f", string(long), "\xff\xfe"} {
		if ValidChat(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !ValidChat(string(long[:MaxChatLength])) {
		t.Error("exactly the max length rejected")
	}
}

func TestChatAndDropBudgets(t *testing.T) {
	var s State
	now := time.Unix(0, 0)
	n := 0
	for s.AllowChat(now) {
		n++
	}
	if n != int(ChatBurst) {
		t.Fatalf("chat burst = %d, want %v", n, ChatBurst)
	}
	if !s.AllowChat(now.Add(time.Second)) {
		t.Fatal("one message a second must pass")
	}
	n = 0
	for s.AllowDrop(now) {
		n++
	}
	if n != int(DropBurst) {
		t.Fatalf("drop burst = %d, want %v", n, DropBurst)
	}
}
