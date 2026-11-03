package server

import (
	"log/slog"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/conn"
	"time"
)

// keepAliveIntervalNanos / keepAliveTimeoutNanos are the live config knobs
// read by every keepAlive goroutine. Held as atomic int64 nanoseconds so
// integration tests can shorten them without tripping `-race` against the
// reads from any keepAlive goroutine still draining cleanup.
//
// Defaults match vanilla: 20s between sends, 30s ceiling before kicking
// a non-responding client.
func (c *ClientConnection) keepAlive() {
	// Use time.After per loop so live updates to conn.KeepAliveInterval take
	// effect on the next iteration. A fixed Ticker would lock the goroutine
	// into the value it had at start time.
	for {
		select {
		case <-c.link.Done():
			return
		case <-time.After(conn.KeepAliveInterval()):
		}

		if c.state != StatePlay || c.isClosed() {
			continue
		}

		// A previous keep-alive still outstanding: the client either lagged
		// past the timeout (→ kick) or just hasn't replied within the
		// current window (→ skip this tick and let it land first).
		id, timedOut, age := c.link.KeepAlive.Next(time.Now())
		if timedOut {
			slog.Warn("keep-alive timeout",
				"player", c.playerName, "id", c.link.KeepAlive.Pending(), "age", age)
			go c.cleanup()
			return
		}
		if id == 0 {
			continue
		}
		if err := c.safeWrite(packetid.CbPlayKeepAlive, protocol.WriteLong(id)); err != nil {
			slog.Warn("keep-alive write failed", "player", c.playerName, "err", err)
			return
		}
	}
}

// onKeepAliveResponse clears the pending state if the client's echo matches
// the ID we sent. False = the id isn't the outstanding one: the server never
// re-sends (a late reply means a timeout kick), so that's a forged echo — the
// caller flags it, and the real outstanding ID stays put.
func (c *ClientConnection) onKeepAliveResponse(id int64) bool {
	return c.link.KeepAlive.Answer(id)
}
