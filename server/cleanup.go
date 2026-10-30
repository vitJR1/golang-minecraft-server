package server

import (
	"log/slog"
	"time"
)

// cleanup closes the connection once: the transport stops taking writes and
// gets a second to flush what's queued (a kick's Disconnect, a status Pong),
// the player leaves its queue and instance, then the socket closes.
func (c *ClientConnection) cleanup() {
	if c.link == nil || !c.link.Shutdown(time.Second) {
		return
	}

	// Drop from any matchmaker queue we might be sitting in.
	if c.server != nil && c.server.Matchmaker != nil {
		c.server.Matchmaker.Dequeue(c)
		c.server.effects.Forget(c)
	}

	// Announce departure + Remove under the same lock as join, but only if
	// the player ever made it into an instance (early disconnects during
	// handshake/login leave c.instance nil).
	if c.instance != nil {
		c.instance.LeaveAndAnnounce(c)
	}

	addr := "bot"
	if a := c.link.RemoteAddr(); a != nil { // bots have no socket
		addr = a.String()
	}
	c.link.Finish()
	slog.Info("connection closed", "addr", addr, "player", c.playerName)
}
