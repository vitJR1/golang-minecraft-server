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

// MaxPacketLength caps the frame length a peer may declare (vanilla's
// 21-bit frame limit, 2 MiB). The length prefix is attacker-controlled, so
// without a cap a single connection could ask us to allocate gigabytes.
const MaxPacketLength = 1 << 21

// MaxUncompressedLength caps the declared pre-compression size of a
// compressed packet (vanilla: 8 MiB). Guards against zlib bombs — a tiny
// wire payload claiming a huge uncompressed size.
const MaxUncompressedLength = 1 << 23

// DebugPackets enables per-packet stderr logging from WritePacket. Leave
// false in production — chunk streaming alone spams thousands of lines/s.
var DebugPackets = false

// ReadPacket reads one framed packet and returns the body buffer (the packet
// ID is the first VarInt inside). If compressionThreshold >= 0, the body is
// in the compressed format (data-length VarInt prefix; payloads at or above
// the threshold were zlib-compressed by the peer).
func ReadPacket(conn io.Reader, compressionThreshold int) (*bytes.Buffer, error) {
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
func ReadPacketSplit(conn io.Reader, compressionThreshold int) (packetID int, payload []byte, err error) {
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
	id := WriteVarInt32(packetID)
	size := len(id) + len(payload) // uncompressed body: VarInt(id) + payload

	if compressionThreshold >= 0 && size >= compressionThreshold {
		// The body (data length + zlib stream) is built after a reserved
		// 5-byte gap — room for the longest VarInt length prefix — so the
		// frame needs one buffer and no copy of the compressed bytes.
		var buf bytes.Buffer
		buf.Grow(maxVarIntLen + maxVarIntLen + size/2 + 64)
		buf.Write(make([]byte, maxVarIntLen))
		buf.Write(WriteVarInt32(int32(size)))
		if err := compressTo(&buf, id, payload); err != nil {
			return nil, fmt.Errorf("compress: %w", err)
		}
		frame := buf.Bytes()
		length := WriteVarInt32(int32(len(frame) - maxVarIntLen))
		start := maxVarIntLen - len(length)
		copy(frame[start:], length)
		return frame[start:], nil
	}

	var dataLen []byte
	if compressionThreshold >= 0 {
		dataLen = WriteVarInt32(0) // below the threshold: sent as is
	}
	bodyLen := len(dataLen) + size
	length := WriteVarInt32(int32(bodyLen))
	frame := make([]byte, 0, len(length)+bodyLen)
	frame = append(frame, length...)
	frame = append(frame, dataLen...)
	frame = append(frame, id...)
	return append(frame, payload...), nil
}

// maxVarIntLen is the longest encoding of a 32-bit VarInt.
const maxVarIntLen = 5

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
func readFramedBody(conn io.Reader) ([]byte, error) {
	length, err := ReadVarIntFromReader(conn)
	if err != nil {
		return nil, fmt.Errorf("packet length: %w", err)
	}
	if length < 0 {
		return nil, errors.New("negative packet length")
	}
	if length > MaxPacketLength {
		return nil, fmt.Errorf("packet length %d exceeds limit %d", length, MaxPacketLength)
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
	if dataLen > MaxUncompressedLength {
		return nil, fmt.Errorf("uncompressed length %d exceeds limit %d", dataLen, MaxUncompressedLength)
	}
	decoded, err := DecompressPayload(rest, dataLen)
	if err != nil {
		return nil, err
	}
	return bytes.NewBuffer(decoded), nil
}
