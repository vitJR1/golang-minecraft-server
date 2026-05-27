package server

import (
	"bytes"
	"fmt"
	"minecraft-server/protocol"
)

func (c *ClientConnection) handlePlay(packet *bytes.Buffer, packetID int) error {
	if c.isClosed() {
		return fmt.Errorf("server closed")
	}

	switch packetID {
	case SbPlayKeepAlive:
		id, err := protocol.ReadLong(packet)
		if err != nil {
			return fmt.Errorf("reading keep alive response: %w", err)
		}
		fmt.Printf("Received keep alive response: %d\n", id)

	case SbPlayChatMessage:
		message, err := protocol.ReadStringFromBuf(packet)
		if err != nil {
			return fmt.Errorf("reading chat message: %w", err)
		}
		fmt.Printf("[CHAT] %s: %s\n", c.playerName, message)
		if !c.isClosed() {
			response := fmt.Sprintf("{\"text\":\"You said: %s\"}", message)
			payload := append(protocol.WriteString(response), 0) // Overlay = false
			_ = c.safeWrite(CbPlaySystemChat, payload)
		}

	case SbPlaySetPos:
		x, y, z, onGround, err := readPosOnGround(packet)
		if err != nil {
			return fmt.Errorf("set player position: %w", err)
		}
		fmt.Printf("Position: %.2f, %.2f, %.2f, onGround=%v\n", x, y, z, onGround)

	case SbPlaySetPosRot:
		x, y, z, err := readXYZ(packet)
		if err != nil {
			return fmt.Errorf("set player position+rotation: %w", err)
		}
		yaw, pitch, onGround, err := readYawPitchOnGround(packet)
		if err != nil {
			return fmt.Errorf("set player position+rotation: %w", err)
		}
		fmt.Printf("Position+Rot: %.2f, %.2f, %.2f, yaw=%.1f, pitch=%.1f, onGround=%v\n",
			x, y, z, yaw, pitch, onGround)

	case SbPlaySetRot:
		yaw, pitch, onGround, err := readYawPitchOnGround(packet)
		if err != nil {
			return fmt.Errorf("set player rotation: %w", err)
		}
		fmt.Printf("Rotation: yaw=%.1f, pitch=%.1f, onGround=%v\n", yaw, pitch, onGround)

	case SbPlayTeleportConfirm:
		id, _ := protocol.ReadVarInt(packet)
		fmt.Println("Teleport confirmed:", id)

	case SbPlayClientInfo:
		// Sent right after LoginSuccess (locale, view distance, chat mode,
		// displayed skin parts, main hand). No-op for now.

	default:
		fmt.Printf("Unknown play packet: 0x%02X, length: %d\n", packetID, packet.Len())
	}
	return nil
}
