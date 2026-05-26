package server

import (
	"fmt"
	"sync/atomic"
)

func (c *ClientConnection) cleanup() {
	if atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		close(c.done)
		c.conn.Close()
		fmt.Printf("Connection from %s closed\n", c.conn.RemoteAddr())
	}
}
