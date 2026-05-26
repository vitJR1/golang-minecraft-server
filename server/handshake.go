package server

import (
	"bytes"
	"fmt"
	"minecraft-server/protocol"
)

func (c *ClientConnection) handleHandshake(packet *bytes.Buffer, packetID int) error {
	if packetID != SbHandshake {
		return fmt.Errorf("expected handshake packet (0x00), got 0x%02X", packetID)
	}

	// protocol version
	_, err := protocol.ReadVarInt(packet)
	if err != nil {
		return fmt.Errorf("reading protocol version: %w", err)
	}

	// server address
	_, err = protocol.ReadStringFromBuf(packet)
	if err != nil {
		return fmt.Errorf("reading server address: %w", err)
	}

	// port
	_, err = protocol.ReadUShortFromBuf(packet)
	if err != nil {
		return fmt.Errorf("reading port: %w", err)
	}

	// next state
	nextState, err := protocol.ReadVarInt(packet)
	if err != nil {
		return fmt.Errorf("reading next state: %w", err)
	}

	switch nextState {
	case 1:
		c.state = StateStatus
		fmt.Println("Switched to status state")
	case 2:
		c.state = StateLogin
		fmt.Println("Switched to login state")
	default:
		return fmt.Errorf("invalid handshake nextState: %d", nextState)
	}

	return nil
}
