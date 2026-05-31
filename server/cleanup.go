package server

import (
	"log/slog"
	"sync/atomic"
	"time"
)

func (c *ClientConnection) cleanup() {
	if !atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		return
	}

	// Close the outbound channel under sendMu so a concurrent safeWrite
	// either fails the isClosed check (taking sendMu after us) or already
	// completed its push (held sendMu before us). Either way no send to
	// closed channel.
	c.sendMu.Lock()
	close(c.outbound)
	c.sendMu.Unlock()

	// Give the writer a moment to drain pending frames (Pong from status
	// handlers, Disconnect messages from kicks) before we tear the socket
	// down. 1s is generous for net.Pipe and TCP localhost; bound it so a
	// stuck client can't keep cleanup hanging.
	select {
	case <-c.writerDone:
	case <-time.After(time.Second):
	}

	// Drop from any matchmaker queue we might be sitting in.
	if c.server != nil && c.server.Matchmaker != nil {
		c.server.Matchmaker.Dequeue(c)
	}

	// Announce departure + Remove under the same lock as join, but only if
	// the player ever made it into an instance (early disconnects during
	// handshake/login leave c.instance nil).
	if c.instance != nil {
		c.instance.LeaveAndAnnounce(c)
	}

	addr := c.conn.RemoteAddr().String()
	c.conn.Close()
	close(c.done)
	slog.Info("connection closed", "addr", addr, "player", c.playerName)
}
