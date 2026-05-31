package server

import (
	"log/slog"
	"minecraft-server/protocol"
	"sync/atomic"
	"time"
)

// keepAliveIntervalNanos / keepAliveTimeoutNanos are the live config knobs
// read by every keepAlive goroutine. Held as atomic int64 nanoseconds so
// integration tests can shorten them without tripping `-race` against the
// reads from any keepAlive goroutine still draining cleanup.
//
// Defaults match vanilla: 20s between sends, 30s ceiling before kicking
// a non-responding client.
var (
	keepAliveIntervalNanos atomic.Int64
	keepAliveTimeoutNanos  atomic.Int64
)

func init() {
	keepAliveIntervalNanos.Store(int64(20 * time.Second))
	keepAliveTimeoutNanos.Store(int64(30 * time.Second))
}

// KeepAliveInterval returns the current send cadence.
func KeepAliveInterval() time.Duration {
	return time.Duration(keepAliveIntervalNanos.Load())
}

// SetKeepAliveInterval changes the send cadence. New value takes effect on
// the next tick of every running keepAlive goroutine.
func SetKeepAliveInterval(d time.Duration) {
	keepAliveIntervalNanos.Store(int64(d))
}

// KeepAliveTimeout returns the current ack ceiling.
func KeepAliveTimeout() time.Duration {
	return time.Duration(keepAliveTimeoutNanos.Load())
}

// SetKeepAliveTimeout changes the ack ceiling. New value takes effect
// immediately for any client already waiting on a response.
func SetKeepAliveTimeout(d time.Duration) {
	keepAliveTimeoutNanos.Store(int64(d))
}

func (c *ClientConnection) keepAlive() {
	// Use time.After per loop so live updates to KeepAliveInterval take
	// effect on the next iteration. A fixed Ticker would lock the goroutine
	// into the value it had at start time.
	for {
		select {
		case <-c.done:
			return
		case <-time.After(KeepAliveInterval()):
		}

		if c.state != StatePlay || c.isClosed() {
			continue
		}

		// If a previous keep-alive is still outstanding, the client either
		// lagged past KeepAliveTimeout (→ kick) or just hasn't replied
		// within the current window (→ skip this tick and let it land
		// before sending another).
		if prevID := c.keepAlivePendingID.Load(); prevID != 0 {
			sentNanos := c.keepAlivePendingSentNanos.Load()
			age := time.Since(time.Unix(0, sentNanos))
			if age > KeepAliveTimeout() {
				slog.Warn("keep-alive timeout",
					"player", c.playerName, "id", prevID, "age", age)
				go c.cleanup()
				return
			}
			continue
		}

		id := time.Now().UnixNano()
		// Stamp the pending state BEFORE the write hits the wire so a fast
		// client can't reply before the handler sees a pending ID.
		c.keepAlivePendingSentNanos.Store(id)
		c.keepAlivePendingID.Store(id)

		if err := c.safeWrite(CbPlayKeepAlive, protocol.WriteLong(id)); err != nil {
			slog.Warn("keep-alive write failed", "player", c.playerName, "err", err)
			return
		}
	}
}

// onKeepAliveResponse clears the pending state if the client's echo matches
// the ID we sent. Stale acks (from before a timeout-induced re-send) are
// ignored — CompareAndSwap fails and the new outstanding ID stays put.
func (c *ClientConnection) onKeepAliveResponse(id int64) {
	c.keepAlivePendingID.CompareAndSwap(id, 0)
}
