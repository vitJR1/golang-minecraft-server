// Package protocol implements the Minecraft Java Edition wire format
// (protocol 763 / 1.20.1) — primitive types, VarInt/VarLong encoding, and
// packet framing.
package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	varIntSegmentBits = 0x7F
	varIntContinueBit = 0x80
)

// ---- VarInt ----

// ReadVarIntFromReader reads a VarInt from an io.Reader (used during packet
// framing, before the body is fully buffered).
func ReadVarIntFromReader(r io.Reader) (int32, error) {
	var result uint32
	var shift uint
	var b [1]byte

	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, fmt.Errorf("VarInt byte: %w", err)
		}
		result |= uint32(b[0]&varIntSegmentBits) << shift
		if b[0]&varIntContinueBit == 0 {
			break
		}
		shift += 7
		if shift > 35 {
			return 0, errors.New("VarInt too long")
		}
	}
	return int32(result), nil
}

// ReadVarInt reads a VarInt from a bytes.Buffer.
func ReadVarInt(buf *bytes.Buffer) (int, error) {
	var value int
	var shift uint
	for {
		if buf.Len() == 0 {
			return 0, errors.New("unexpected end of buffer while reading VarInt")
		}
		b, err := buf.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= int(b&varIntSegmentBits) << shift
		shift += 7
		if b&varIntContinueBit == 0 {
			break
		}
		if shift > 35 {
			return 0, errors.New("VarInt too long")
		}
	}
	return value, nil
}

// ReadVarIntFromBytes reads a VarInt from a raw byte slice, returning the
// parsed value and the number of bytes consumed.
func ReadVarIntFromBytes(data []byte) (value, bytesRead int, err error) {
	for {
		if bytesRead >= len(data) {
			return 0, bytesRead, errors.New("insufficient data for VarInt")
		}
		b := data[bytesRead]
		value |= int(b&varIntSegmentBits) << (7 * bytesRead)
		bytesRead++
		if b&varIntContinueBit == 0 {
			break
		}
		if bytesRead > 5 {
			return 0, bytesRead, errors.New("VarInt too long")
		}
	}
	return value, bytesRead, nil
}

func WriteVarInt32(value int32) []byte {
	buf := make([]byte, 0, 5)
	u := uint32(value)
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if u == 0 {
			break
		}
	}
	return buf
}

func WriteVarInt32ToBuffer(buf *bytes.Buffer, value int32) {
	u := uint32(value)
	for {
		b := byte(u & varIntSegmentBits)
		u >>= 7
		if u != 0 {
			b |= varIntContinueBit
		}
		buf.WriteByte(b)
		if u == 0 {
			break
		}
	}
}

// ---- Strings and byte arrays ----

func ReadStringFromBuf(buf *bytes.Buffer) (string, error) {
	length, err := ReadVarInt(buf)
	if err != nil {
		return "", fmt.Errorf("string length: %w", err)
	}
	if length < 0 {
		return "", errors.New("negative string length")
	}
	if buf.Len() < length {
		return "", fmt.Errorf("insufficient data for string: need %d, have %d", length, buf.Len())
	}
	strBytes := make([]byte, length)
	if _, err := buf.Read(strBytes); err != nil {
		return "", fmt.Errorf("string data: %w", err)
	}
	return string(strBytes), nil
}

func WriteString(s string) []byte {
	data := []byte(s)
	if len(data) > 32767 {
		panic(fmt.Sprintf("string too long: %d bytes", len(data)))
	}
	length := WriteVarInt32(int32(len(data)))
	out := make([]byte, 0, len(length)+len(data))
	out = append(out, length...)
	out = append(out, data...)
	return out
}

func ReadByteArrayFromBuf(buf *bytes.Buffer) ([]byte, error) {
	length, err := ReadVarInt(buf)
	if err != nil {
		return nil, fmt.Errorf("byte array length: %w", err)
	}
	if length < 0 {
		return nil, errors.New("negative byte array length")
	}
	if buf.Len() < length {
		return nil, fmt.Errorf("insufficient data for byte array: need %d, have %d", length, buf.Len())
	}
	data := make([]byte, length)
	if _, err := buf.Read(data); err != nil {
		return nil, fmt.Errorf("byte array data: %w", err)
	}
	return data, nil
}

// ---- Fixed-width numerics (big-endian) ----

func ReadLong(buf *bytes.Buffer) (int64, error) {
	if buf.Len() < 8 {
		return 0, fmt.Errorf("insufficient data for long: need 8, have %d", buf.Len())
	}
	var value int64
	if err := binary.Read(buf, binary.BigEndian, &value); err != nil {
		return 0, fmt.Errorf("long: %w", err)
	}
	return value, nil
}

func WriteLong(value int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(value))
	return buf
}

func ReadDouble(buf *bytes.Buffer) (float64, error) {
	if buf.Len() < 8 {
		return 0, fmt.Errorf("insufficient data for double: need 8, have %d", buf.Len())
	}
	var value float64
	if err := binary.Read(buf, binary.BigEndian, &value); err != nil {
		return 0, fmt.Errorf("double: %w", err)
	}
	return value, nil
}

func WriteDouble(value float64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(value))
	return buf
}

func ReadFloat(buf *bytes.Buffer) (float32, error) {
	if buf.Len() < 4 {
		return 0, fmt.Errorf("insufficient data for float: need 4, have %d", buf.Len())
	}
	var bits uint32
	if err := binary.Read(buf, binary.BigEndian, &bits); err != nil {
		return 0, fmt.Errorf("float: %w", err)
	}
	return math.Float32frombits(bits), nil
}

func WriteFloat(value float32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, math.Float32bits(value))
	return buf
}

func WriteShort(value int16) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(value))
	return buf
}

func WriteInt(value int32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(value))
	return buf
}

// WritePosition encodes a block position into 8 bytes using the Minecraft
// protocol's packed Position format: x (26 bits) | z (26 bits) | y (12 bits)
// laid out as a single big-endian Long.
//
//	bits 63..38: x (signed, two's complement to 26 bits)
//	bits 37..12: z (signed, two's complement to 26 bits)
//	bits 11..0 : y (signed, two's complement to 12 bits, range -2048..2047)
func WritePosition(x, y, z int) []byte {
	encoded := ((int64(x) & 0x3FFFFFF) << 38) |
		((int64(z) & 0x3FFFFFF) << 12) |
		(int64(y) & 0xFFF)
	return WriteLong(encoded)
}

func ReadUShortFromBuf(buf *bytes.Buffer) (uint16, error) {
	if buf.Len() < 2 {
		return 0, fmt.Errorf("insufficient data for ushort: need 2, have %d", buf.Len())
	}
	var value uint16
	if err := binary.Read(buf, binary.BigEndian, &value); err != nil {
		return 0, err
	}
	return value, nil
}

func ReadBool(buf *bytes.Buffer) (bool, error) {
	if buf.Len() < 1 {
		return false, fmt.Errorf("insufficient data for bool: need 1, have %d", buf.Len())
	}
	b, err := buf.ReadByte()
	if err != nil {
		return false, fmt.Errorf("bool: %w", err)
	}
	return b != 0, nil
}
