package server

// isClosed reports whether the connection is closing or closed.
func (c *ClientConnection) isClosed() bool { return c.link != nil && c.link.Closed() }
