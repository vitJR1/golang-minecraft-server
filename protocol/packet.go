package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
)

// CompressionDisabled is the sentinel value for the compressionThreshold
// argument: while a connection hasn't received Set Compression yet, packets
// use the simpler uncompressed framing.
const CompressionDisabled = -1

// DebugPackets enables per-packet stderr logging from WritePacket. Leave
// false in production — chunk streaming alone spams thousands of lines/s.
var DebugPackets = false

// ReadPacket reads one framed packet and returns the body buffer (the packet
// ID is the first VarInt inside). If compressionThreshold >= 0, the body is
// in the compressed format (data-length VarInt prefix; payloads at or above
// the threshold were zlib-compressed by the peer).
func ReadPacket(conn net.Conn, compressionThreshold int) (*bytes.Buffer, error) {
	body, err := readFramedBody(conn)
	if err != nil {
		return nil, err
	}
	if compressionThreshold < 0 {
		return bytes.NewBuffer(body), nil
	}
	return decodeCompressedBody(body)
}

// ReadPacketSplit reads a packet and returns its ID and remaining payload.
func ReadPacketSplit(conn net.Conn, compressionThreshold int) (packetID int, payload []byte, err error) {
	body, err := readFramedBody(conn)
	if err != nil {
		return 0, nil, err
	}
	if compressionThreshold >= 0 {
		buf, err := decodeCompressedBody(body)
		if err != nil {
			return 0, nil, err
		}
		body = buf.Bytes()
	}
	id, n, err := ReadVarIntFromBytes(body)
	if err != nil {
		return 0, nil, fmt.Errorf("packet ID: %w", err)
	}
	if n == 0 {
		return 0, nil, errors.New("no bytes read for packet ID")
	}
	return id, body[n:], nil
}

// BuildFrame composes a complete packet frame ready to be written to the
// wire: VarInt(length) + VarInt(packetID) + payload, with optional zlib
// compression. Same rules as WritePacket; the only difference is this
// returns bytes instead of touching a conn — useful when a single payload
// is broadcast to many connections (compress once, write many).
func BuildFrame(packetID int32, payload []byte, compressionThreshold int) ([]byte, error) {
	uncompressed := append(WriteVarInt32(packetID), payload...)

	var body []byte
	if compressionThreshold < 0 {
		body = uncompressed
	} else if len(uncompressed) < compressionThreshold {
		body = append(WriteVarInt32(0), uncompressed...)
	} else {
		compressed, err := CompressPayload(uncompressed)
		if err != nil {
			return nil, fmt.Errorf("compress: %w", err)
		}
		body = append(WriteVarInt32(int32(len(uncompressed))), compressed...)
	}

	length := WriteVarInt32(int32(len(body)))
	return append(length, body...), nil
}

// WritePacket frames and writes a single packet. Equivalent to BuildFrame +
// conn.Write with partial-write handling.
func WritePacket(conn net.Conn, packetID int32, payload []byte, compressionThreshold int) error {
	full, err := BuildFrame(packetID, payload, compressionThreshold)
	if err != nil {
		return err
	}

	if DebugPackets {
		fmt.Printf("Sending packet 0x%02X, payload=%d, total=%d, compressed=%v\n",
			packetID, len(payload), len(full), compressionThreshold >= 0)
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

// readFramedBody reads a VarInt-prefixed packet body off the wire. Used by
// both compressed and uncompressed paths.
func readFramedBody(conn net.Conn) ([]byte, error) {
	length, err := ReadVarIntFromReader(conn)
	if err != nil {
		return nil, fmt.Errorf("packet length: %w", err)
	}
	if length < 0 {
		return nil, errors.New("negative packet length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, fmt.Errorf("packet data: %w", err)
	}
	return body, nil
}

// decodeCompressedBody interprets a compressed-format packet body: a
// data-length VarInt followed by either raw bytes (length == 0) or
// zlib-compressed bytes (length == uncompressed size).
func decodeCompressedBody(body []byte) (*bytes.Buffer, error) {
	dataLen, n, err := ReadVarIntFromBytes(body)
	if err != nil {
		return nil, fmt.Errorf("data length: %w", err)
	}
	rest := body[n:]
	if dataLen == 0 {
		return bytes.NewBuffer(rest), nil
	}
	if dataLen < 0 {
		return nil, errors.New("negative compressed data length")
	}
	decoded, err := DecompressPayload(rest, dataLen)
	if err != nil {
		return nil, err
	}
	return bytes.NewBuffer(decoded), nil
}
