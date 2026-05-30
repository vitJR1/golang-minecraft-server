package server

import (
	"log/slog"
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
				slog.Warn("keep-alive write failed", "player", c.playerName, "err", err)
				return
			}
		}
	}
}
