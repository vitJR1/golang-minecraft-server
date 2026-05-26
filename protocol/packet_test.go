package protocol

import (
	"bytes"
	"io"
	"net"
	"testing"
)

// pipePair returns the two ends of a net.Pipe() and registers cleanup.
func pipePair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b
}

func TestWritePacketReadPacketRoundTrip(t *testing.T) {
	cli, srv := pipePair(t)

	go func() {
		_ = WritePacket(srv, 0x05, []byte{0xaa, 0xbb, 0xcc}, CompressionDisabled)
	}()

	buf, err := ReadPacket(cli, CompressionDisabled)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ReadVarInt(buf)
	if err != nil {
		t.Fatal(err)
	}
	if id != 5 {
		t.Errorf("packet id: got %d, want 5", id)
	}
	if !bytes.Equal(buf.Bytes(), []byte{0xaa, 0xbb, 0xcc}) {
		t.Errorf("payload: %x", buf.Bytes())
	}
}

func TestReadPacketSplit(t *testing.T) {
	cli, srv := pipePair(t)

	go func() {
		_ = WritePacket(srv, 0x42, []byte{0x11, 0x22}, CompressionDisabled)
	}()

	id, payload, err := ReadPacketSplit(cli, CompressionDisabled)
	if err != nil {
		t.Fatal(err)
	}
	if id != 0x42 {
		t.Errorf("id: got 0x%02X, want 0x42", id)
	}
	if !bytes.Equal(payload, []byte{0x11, 0x22}) {
		t.Errorf("payload: %x", payload)
	}
}

func TestReadPacketEmptyPayload(t *testing.T) {
	cli, srv := pipePair(t)

	go func() {
		_ = WritePacket(srv, 0x00, nil, CompressionDisabled)
	}()

	buf, err := ReadPacket(cli, CompressionDisabled)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := ReadVarInt(buf)
	if id != 0 || buf.Len() != 0 {
		t.Errorf("expected id=0 and empty payload, got id=%d remaining=%d", id, buf.Len())
	}
}

func TestReadPacketNegativeLength(t *testing.T) {
	cli, srv := pipePair(t)

	go func() {
		// Write VarInt of -1 as a length prefix — server should refuse.
		_, _ = srv.Write(WriteVarInt32(-1))
	}()

	if _, err := ReadPacket(cli, CompressionDisabled); err == nil {
		t.Fatal("expected error for negative packet length")
	}
}

func TestWritePacketLengthHeader(t *testing.T) {
	// Manually decode the framing to confirm the length prefix matches body size.
	cli, srv := pipePair(t)
	go func() {
		_ = WritePacket(srv, 0x10, []byte{1, 2, 3, 4, 5}, CompressionDisabled)
	}()

	// Read the raw frame: VarInt(length) + body.
	length, err := ReadVarIntFromReader(cli)
	if err != nil {
		t.Fatal(err)
	}
	// body = VarInt(packetID=0x10, 1 byte) + 5 payload bytes = 6
	if length != 6 {
		t.Errorf("framing length: got %d, want 6", length)
	}
}

func TestCompressedRoundTripBelowThreshold(t *testing.T) {
	cli, srv := pipePair(t)
	const threshold = 256

	go func() {
		_ = WritePacket(srv, 0x07, []byte{1, 2, 3}, threshold)
	}()
	buf, err := ReadPacket(cli, threshold)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := ReadVarInt(buf)
	if id != 7 {
		t.Errorf("id: got %d, want 7", id)
	}
	if !bytes.Equal(buf.Bytes(), []byte{1, 2, 3}) {
		t.Errorf("payload: %x", buf.Bytes())
	}
}

func TestCompressedRoundTripAboveThreshold(t *testing.T) {
	cli, srv := pipePair(t)
	const threshold = 256
	// Build a payload bigger than threshold so the compressed path actually runs.
	payload := bytes.Repeat([]byte("ABCDEFGH"), 100) // 800 bytes

	go func() {
		_ = WritePacket(srv, 0x09, payload, threshold)
	}()
	buf, err := ReadPacket(cli, threshold)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := ReadVarInt(buf)
	if id != 9 {
		t.Errorf("id: got %d, want 9", id)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Errorf("payload size %d: round-trip mismatch", len(payload))
	}
}

func TestCompressedFramingByteLayoutBelowThreshold(t *testing.T) {
	// When below threshold, body = VarInt(0) + raw [packetID + payload].
	// Verify by reading the raw frame.
	cli, srv := pipePair(t)
	const threshold = 256

	go func() {
		_ = WritePacket(srv, 0x03, []byte{0xAA}, threshold)
	}()

	bodyLen, err := ReadVarIntFromReader(cli)
	if err != nil {
		t.Fatal(err)
	}
	// data length VarInt = 1 byte (value 0)
	// packetID VarInt = 1 byte (0x03)
	// payload = 1 byte
	// total = 3 bytes
	if bodyLen != 3 {
		t.Errorf("body length: got %d, want 3", bodyLen)
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(cli, body); err != nil {
		t.Fatal(err)
	}
	if body[0] != 0 { // data length = 0 (uncompressed)
		t.Errorf("data length byte: got 0x%02x, want 0x00", body[0])
	}
	if body[1] != 0x03 {
		t.Errorf("packet ID byte: got 0x%02x, want 0x03", body[1])
	}
}
