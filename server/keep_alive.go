package server

import (
	"fmt"
	"minecraft-server/protocol"
	"time"
)

func (c *ClientConnection) keepAlive() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if c.state != StatePlay || c.isClosed() {
				continue
			}

			keepAliveID := time.Now().UnixNano()
			payload := protocol.WriteLong(keepAliveID)

			err := c.safeWrite(CbPlayKeepAlive, payload)
			if err != nil {
				fmt.Printf("Keep-alive error: %v\n", err)
				return
			}
		}
	}
}
