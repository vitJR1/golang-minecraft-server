package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"minecraft-server/protocol"
)

func (c *ClientConnection) handleStatus(packet *bytes.Buffer, packetID int) error {
	switch packetID {
	case SbStatusRequest:
		slog.Debug("status request", "addr", c.conn.RemoteAddr().String())
		resp := map[string]any{
			"version": map[string]any{
				"name":     "1.20.1",
				"protocol": 763,
			},
			"players": map[string]any{
				"max":    20,
				"online": 0,
			},
			"description": map[string]any{
				"text": "§aGoLang test server 🚀",
			},
		}
		data, _ := json.Marshal(resp)
		return c.safeWrite(CbStatusResponse, protocol.WriteString(string(data)))

	case SbStatusPing:
		slog.Debug("status ping", "addr", c.conn.RemoteAddr().String())
		payload := make([]byte, 8)
		if _, err := packet.Read(payload); err != nil {
			return fmt.Errorf("reading ping payload: %w", err)
		}
		if err := c.safeWrite(CbStatusPong, payload); err != nil {
			return err
		}
		// Status handshakes are one-shot — close after pong.
		c.cleanup()
		return nil

	default:
		return fmt.Errorf("unknown status packet: 0x%02X", packetID)
	}
}
