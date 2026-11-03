package server

import (
	"bytes"
	"fmt"
	"log/slog"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/packets"
)

func (c *ClientConnection) handleHandshake(packet *bytes.Buffer, packetID int) error {
	if packetID != packetid.SbHandshake {
		return fmt.Errorf("expected handshake packet (0x00), got 0x%02X", packetID)
	}

	h, err := packets.ReadHandshake(packet)
	if err != nil {
		return err
	}

	switch h.Next {
	case packets.NextStatus:
		c.state = StateStatus
		slog.Debug("state → status", "addr", c.link.RemoteAddr().String())
	case packets.NextLogin:
		c.state = StateLogin
		slog.Debug("state → login", "addr", c.link.RemoteAddr().String())
	default:
		return fmt.Errorf("invalid handshake nextState: %d", h.Next)
	}

	return nil
}
