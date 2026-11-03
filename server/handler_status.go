package server

import (
	"bytes"
	"fmt"
	"log/slog"
	"minecraft-server/cfg"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/favicon"
	"minecraft-server/server/net/status"
)

func (c *ClientConnection) handleStatus(packet *bytes.Buffer, packetID int) error {
	switch packetID {
	case packetid.SbStatusRequest:
		slog.Debug("status request", "addr", c.link.RemoteAddr().String())
		data := status.Response(status.Info{
			Online:  c.server.PlayerCount(),
			Max:     cfg.MaxPlayers,
			Names:   c.server.PlayerNames(),
			Favicon: favicon.Current(), // "" unless LoadFavicon found a PNG at startup
		})
		return c.safeWrite(packetid.CbStatusResponse, protocol.WriteString(string(data)))

	case packetid.SbStatusPing:
		slog.Debug("status ping", "addr", c.link.RemoteAddr().String())
		payload := make([]byte, 8)
		if _, err := packet.Read(payload); err != nil {
			return fmt.Errorf("reading ping payload: %w", err)
		}
		if err := c.safeWrite(packetid.CbStatusPong, payload); err != nil {
			return err
		}
		// Status handshakes are one-shot — close after pong.
		c.cleanup()
		return nil

	default:
		return fmt.Errorf("unknown status packet: 0x%02X", packetID)
	}
}
