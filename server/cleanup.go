package server

import (
	"fmt"
	"sync/atomic"
)

func (c *ClientConnection) cleanup() {
	if !atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		return
	}
	// Drop the player from the server-wide list. c.player may be nil if
	// cleanup ran before login completed (e.g. handshake error); Remove on
	// an absent entity ID is a no-op even when the player is set but never
	// got registered, so there's no need to mirror handler_login's exact
	// sequence here.
	if c.player != nil {
		c.server.Players.Remove(c.player.EntityID)
	}
	close(c.done)
	c.conn.Close()
	fmt.Printf("Connection from %s closed\n", c.conn.RemoteAddr())
}
