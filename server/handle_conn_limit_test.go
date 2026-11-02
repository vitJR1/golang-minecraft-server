package server

import (
	"minecraft-server/cfg"
	"testing"
	"time"
)

// withConnCaps temporarily overrides the flood-limit config for one test.
func withConnCaps(t *testing.T, total, perIP, rate int) {
	t.Helper()
	prevTotal, prevPerIP, prevRate := cfg.MaxConns, cfg.MaxConnsPerIP, cfg.ConnRatePerIP
	cfg.MaxConns, cfg.MaxConnsPerIP, cfg.ConnRatePerIP = total, perIP, rate
	t.Cleanup(func() {
		cfg.MaxConns, cfg.MaxConnsPerIP, cfg.ConnRatePerIP = prevTotal, prevPerIP, prevRate
	})
}

// TestHandleConnRejectsOverLimit drives the real HandleConn path: with a
// per-IP cap of 1, a second pipe client on the same Server must be closed
// immediately (all net.Pipe conns share the synthetic "pipe" IP).
func TestHandleConnRejectsOverLimit(t *testing.T) {
	withConnCaps(t, 0, 1, 0)
	s := New()

	_ = pipeClientOn(t, s) // first client occupies the single per-IP slot

	second := pipeClientOn(t, s)
	// The rejected side is closed without any response; a read must fail.
	second.Conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1)
	if _, err := second.Conn.Read(buf); err == nil {
		t.Fatal("expected the second connection to be closed by the limiter")
	}
}
