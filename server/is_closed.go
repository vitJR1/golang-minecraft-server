package server

import "sync/atomic"

func (c *ClientConnection) isClosed() bool {
	return atomic.LoadInt32(&c.closed) == 1
}
