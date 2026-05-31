package server

import (
	"encoding/binary"
	"minecraft-server/protocol"
	"testing"
	"time"
)

// withKeepAliveTiming compresses the keep-alive interval and timeout for
// the duration of one test, restoring them on cleanup. The Set* helpers
// are atomic, so this is safe to call while other goroutines (from prior
// tests) are mid-loop.
func withKeepAliveTiming(t *testing.T, interval, timeout time.Duration) {
	t.Helper()
	origI, origT := KeepAliveInterval(), KeepAliveTimeout()
	SetKeepAliveInterval(interval)
	SetKeepAliveTimeout(timeout)
	t.Cleanup(func() {
		SetKeepAliveInterval(origI)
		SetKeepAliveTimeout(origT)
	})
}

// TestKeepAliveKicksUnresponsiveClient drives the keepAlive goroutine on
// compressed timings: the server should send a Keep Alive, see no reply,
// then drop the player after KeepAliveTimeout.
func TestKeepAliveKicksUnresponsiveClient(t *testing.T) {
	withKeepAliveTiming(t, 30*time.Millisecond, 90*time.Millisecond)

	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Zombie")

	// Start draining the wire — must consume any packets the server sends
	// so the synchronous net.Pipe doesn't back up. We intentionally do NOT
	// echo any Keep Alive responses.
	go func() {
		for {
			if _, err := protocol.ReadPacket(cli.conn, cli.threshold); err != nil {
				return
			}
		}
	}()

	// Interval=30ms, timeout=90ms → first KA goes out at ~30ms, then on the
	// next ticks it sees the pending ID > timeout and kicks. Allow generous
	// slack so a slow CI doesn't flake.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Hub.Players.Count() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected unresponsive client to be kicked, still in player list (count=%d)",
		s.Hub.Players.Count())
}

// TestKeepAliveAckKeepsClientAlive: a well-behaved client echoes every
// Keep Alive it receives and stays in the player list across several
// intervals.
func TestKeepAliveAckKeepsClientAlive(t *testing.T) {
	withKeepAliveTiming(t, 30*time.Millisecond, 90*time.Millisecond)

	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Healthy")

	// Drainer that responds to every Keep Alive in-process.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			buf, err := protocol.ReadPacket(cli.conn, cli.threshold)
			if err != nil {
				return
			}
			id, err := protocol.ReadVarInt(buf)
			if err != nil {
				return
			}
			if id != CbPlayKeepAlive {
				continue
			}
			payload := buf.Next(8)
			if len(payload) != 8 {
				return
			}
			keepID := int64(binary.BigEndian.Uint64(payload))
			// Echo it back.
			if err := protocol.WritePacket(cli.conn, int32(SbPlayKeepAlive),
				protocol.WriteLong(keepID), cli.threshold); err != nil {
				return
			}
		}
	}()

	// Sit through ~6 intervals — if ack logic is broken the player gets
	// kicked well before this.
	time.Sleep(200 * time.Millisecond)
	if s.Hub.Players.Count() != 1 {
		t.Fatalf("healthy client got kicked: count=%d", s.Hub.Players.Count())
	}

	_ = cli.conn.Close()
	<-done
}

// TestKeepAliveStaleAckIgnored: an ack with the wrong ID must NOT clear
// the pending state — otherwise a misbehaving client could keep itself
// alive forever by spamming bogus IDs.
func TestKeepAliveStaleAckIgnored(t *testing.T) {
	c := &ClientConnection{}
	c.keepAlivePendingID.Store(42)
	c.onKeepAliveResponse(99) // wrong ID
	if c.keepAlivePendingID.Load() != 42 {
		t.Errorf("stale ack cleared pending: got %d, want 42", c.keepAlivePendingID.Load())
	}
	c.onKeepAliveResponse(42) // correct ID
	if c.keepAlivePendingID.Load() != 0 {
		t.Errorf("matching ack did not clear: got %d, want 0", c.keepAlivePendingID.Load())
	}
}
