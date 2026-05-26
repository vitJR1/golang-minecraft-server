package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
)

// DebugPackets enables per-packet stderr logging from WritePacket. Leave
// false in production — chunk streaming alone spams thousands of lines/s.
var DebugPackets = false

// ReadPacket reads one length-prefixed packet and returns the body buffer
// (the packet ID is still the first VarInt inside it).
func ReadPacket(conn net.Conn) (*bytes.Buffer, error) {
	length, err := ReadVarIntFromReader(conn)
	if err != nil {
		return nil, fmt.Errorf("packet length: %w", err)
	}
	if length < 0 {
		return nil, errors.New("negative packet length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("packet data: %w", err)
	}
	return bytes.NewBuffer(data), nil
}

// ReadPacketSplit reads a packet and returns its ID and the remaining payload
// bytes. Used when a caller wants the ID surfaced without re-parsing.
func ReadPacketSplit(conn net.Conn) (packetID int, payload []byte, err error) {
	length, err := ReadVarIntFromReader(conn)
	if err != nil {
		return 0, nil, fmt.Errorf("packet length: %w", err)
	}
	if length < 0 {
		return 0, nil, errors.New("negative packet length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return 0, nil, fmt.Errorf("packet data: %w", err)
	}
	id, n, err := ReadVarIntFromBytes(data)
	if err != nil {
		return 0, nil, fmt.Errorf("packet ID: %w", err)
	}
	if n == 0 {
		return 0, nil, errors.New("no bytes read for packet ID")
	}
	return id, data[n:], nil
}

// WritePacket frames and writes a single packet. Format on the wire is
// VarInt(length) + VarInt(packetID) + payload.
func WritePacket(conn net.Conn, packetID int32, payload []byte) error {
	id := WriteVarInt32(packetID)
	body := append(id, payload...)
	length := WriteVarInt32(int32(len(body)))
	full := append(length, body...)

	if DebugPackets {
		fmt.Printf("Sending packet 0x%02X, payload=%d, total=%d\n", packetID, len(payload), len(full))
	}

	written := 0
	for written < len(full) {
		n, err := conn.Write(full[written:])
		if err != nil {
			return fmt.Errorf("write: %w", err)
		}
		if n == 0 {
			return errors.New("wrote 0 bytes")
		}
		written += n
	}
	return nil
}
