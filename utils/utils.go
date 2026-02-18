package utils

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
)

const SegmentBits = 0x7F
const ContinueBit = 0x80

// ReadPacket читает полный пакет из соединения (длина + данные)
func ReadPacket(conn net.Conn) (*bytes.Buffer, error) {
	length, err := ReadVarIntFromReader(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to read packet length: %w", err)
	}

	if length < 0 {
		return nil, errors.New("negative packet length")
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("failed to read packet data: %w", err)
	}

	return bytes.NewBuffer(data), nil
}

// ReadPacket2 читает пакет и возвращает ID и данные отдельно
func ReadPacket2(conn net.Conn) (packetID int, data []byte, err error) {
	// читаем длину пакета (VarInt)
	length, err := ReadVarIntFromReader(conn)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read packet length 2: %w", err)
	}

	if length < 0 {
		return 0, nil, errors.New("negative packet length")
	}

	packetData := make([]byte, length)
	if _, err := io.ReadFull(conn, packetData); err != nil {
		return 0, nil, fmt.Errorf("failed to read packet data: %w", err)
	}

	// первый VarInt — ID пакета
	packetID, bytesRead, err := ReadVarIntFromBytes(packetData)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read packet ID: %w", err)
	}
	if bytesRead == 0 {
		return 0, nil, errors.New("no bytes read for packet ID")
	}

	// возвращаем ID и оставшиеся данные
	return packetID, packetData[bytesRead:], nil
}

// WritePacket отправляет пакет в соединение
func WritePacket(conn net.Conn, packetID int32, payload []byte) error {
	id := WriteVarInt32(packetID)
	body := append(id, payload...)
	length := WriteVarInt32(int32(len(body)))

	fullPacket := append(length, body...)

	fmt.Printf("Sending packet 0x%02X, payload=%d, total=%d\n", packetID, len(payload), len(fullPacket))

	// writeAll
	written := 0
	for written < len(fullPacket) {
		n, err := conn.Write(fullPacket[written:])
		if err != nil {
			return fmt.Errorf("failed to write packet: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("failed to write packet: wrote 0 bytes")
		}
		written += n
	}
	return nil
}

// ReadVarIntFromReader читает VarInt из io.Reader
func ReadVarIntFromReader(r io.Reader) (int32, error) {
	var result uint32
	var shift uint
	var b [1]byte

	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, fmt.Errorf("failed to read VarInt byte: %w", err)
		}
		byteVal := b[0]
		result |= uint32(byteVal&SegmentBits) << shift

		if byteVal&ContinueBit == 0 {
			break
		}
		shift += 7
		if shift > 35 {
			return 0, errors.New("VarInt is too long")
		}
	}

	return int32(result), nil
}

// ReadVarInt читает VarInt из bytes.Buffer
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

		value |= int(b&SegmentBits) << shift
		shift += 7

		if b&ContinueBit == 0 {
			break
		}

		if shift > 35 {
			return 0, errors.New("VarInt is too long")
		}
	}

	return value, nil
}

func ReadVarInt32(buf *bytes.Buffer) (int32, error) {
	var result uint32
	var shift uint

	for {
		if buf.Len() == 0 {
			return 0, errors.New("unexpected end of buffer while reading VarInt")
		}
		b, err := buf.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= uint32(b&SegmentBits) << shift

		if b&ContinueBit == 0 {
			break
		}
		shift += 7
		if shift > 35 {
			return 0, errors.New("VarInt is too long")
		}
	}
	return int32(result), nil
}

// ReadVarIntFromBytes читает VarInt из среза байт
func ReadVarIntFromBytes(data []byte) (value int, bytesRead int, err error) {
	var numRead int
	var result int

	for {
		if numRead >= len(data) {
			return 0, numRead, errors.New("insufficient data for VarInt")
		}

		b := data[numRead]
		result |= int(b&SegmentBits) << (7 * numRead)
		numRead++

		if (b & ContinueBit) == 0 {
			break
		}

		if numRead > 5 {
			return 0, numRead, errors.New("VarInt is too long")
		}
	}

	return result, numRead, nil
}

// WriteVarInt32 записывает int32 как VarInt
func WriteVarInt32(value int32) []byte {
	buf := make([]byte, 0, 5)
	u := uint32(value)

	for {
		b := byte(u & SegmentBits)
		u >>= 7
		if u != 0 {
			b |= ContinueBit
		}
		buf = append(buf, b)
		if u == 0 {
			break
		}
	}

	return buf
}

// WriteVarInt64 записывает int64 как VarInt
func WriteVarInt64(value int64) []byte {
	buf := make([]byte, 0, 10)
	u := uint64(value)

	for {
		b := byte(u & SegmentBits)
		u >>= 7
		if u != 0 {
			b |= ContinueBit
		}
		buf = append(buf, b)
		if u == 0 {
			break
		}
	}

	return buf
}

// ReadString читает строку с префиксом VarInt из io.Reader
func ReadString(r io.Reader) (string, error) {
	length, err := ReadVarIntFromReader(r)
	if err != nil {
		return "", fmt.Errorf("failed to read string length: %w", err)
	}

	if length < 0 {
		return "", errors.New("negative string length")
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("failed to read string data: %w", err)
	}

	return string(buf), nil
}

// ReadStringFromBuf читает строку с префиксом VarInt из bytes.Buffer
func ReadStringFromBuf(buf *bytes.Buffer) (string, error) {
	length, err := ReadVarInt(buf)
	if err != nil {
		return "", fmt.Errorf("failed to read string length: %w", err)
	}

	if length < 0 {
		return "", errors.New("negative string length")
	}

	if buf.Len() < length {
		return "", fmt.Errorf("insufficient data for string: need %d, have %d", length, buf.Len())
	}

	strBytes := make([]byte, length)
	if _, err := buf.Read(strBytes); err != nil {
		return "", fmt.Errorf("failed to read string data: %w", err)
	}

	return string(strBytes), nil
}

// WriteString записывает строку с префиксом VarInt длины
func WriteString(s string) []byte {
	data := []byte(s)
	length := WriteVarInt32(int32(len(data)))
	return append(length, data...)
}

// ReadUShort читает uint16 из io.Reader
func ReadUShort(r io.Reader) (uint16, error) {
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, fmt.Errorf("failed to read uint16: %w", err)
	}
	return binary.BigEndian.Uint16(buf[:]), nil
}

// WriteUInt16 записывает uint16 как 2 байта (big endian)
func WriteUInt16(value uint16) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, value)
	return buf
}

// ReadByteArray читает массив байт с префиксом VarInt
func ReadByteArray(data []byte) (out []byte, rest []byte, err error) {
	length, bytesRead, err := ReadVarIntFromBytes(data)
	if err != nil {
		return nil, data, fmt.Errorf("failed to read byte array length: %w", err)
	}
	if bytesRead == 0 {
		return nil, data, errors.New("no bytes read for byte array length")
	}
	if length < 0 {
		return nil, data, errors.New("negative byte array length")
	}

	if len(data) < bytesRead+length {
		return nil, data, fmt.Errorf("insufficient data for byte array: need %d, have %d", bytesRead+length, len(data))
	}

	out = data[bytesRead : bytesRead+length]
	rest = data[bytesRead+length:]
	return out, rest, nil
}

// ReadByteArrayFromBuf читает массив байт с префиксом VarInt из bytes.Buffer
func ReadByteArrayFromBuf(buf *bytes.Buffer) ([]byte, error) {
	length, err := ReadVarInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read byte array length: %w", err)
	}
	if length < 0 {
		return nil, errors.New("negative byte array length")
	}
	if buf.Len() < length {
		return nil, fmt.Errorf("insufficient data for byte array: need %d, have %d", length, buf.Len())
	}

	data := make([]byte, length)
	if _, err := buf.Read(data); err != nil {
		return nil, fmt.Errorf("failed to read byte array data: %w", err)
	}
	return data, nil
}

// ReadLong читает int64 из bytes.Buffer
func ReadLong(buf *bytes.Buffer) (int64, error) {
	if buf.Len() < 8 {
		return 0, fmt.Errorf("insufficient data for long: need 8, have %d", buf.Len())
	}

	var value int64
	if err := binary.Read(buf, binary.BigEndian, &value); err != nil {
		return 0, fmt.Errorf("failed to read long: %w", err)
	}
	return value, nil
}

// WriteLong записывает int64 как 8 байт (big endian)
func WriteLong(value int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(value))
	return buf
}

// ReadDouble читает float64 из bytes.Buffer
func ReadDouble(buf *bytes.Buffer) (float64, error) {
	if buf.Len() < 8 {
		return 0, fmt.Errorf("insufficient data for double: need 8, have %d", buf.Len())
	}

	var value float64
	if err := binary.Read(buf, binary.BigEndian, &value); err != nil {
		return 0, fmt.Errorf("failed to read double: %w", err)
	}
	return value, nil
}

// WriteDouble записывает float64 как 8 байт (big endian)
func WriteDouble(value float64) []byte {
	buf := make([]byte, 8)
	bits := math.Float64bits(value)
	binary.BigEndian.PutUint64(buf, bits)
	return buf
}

// ReadFloat читает float32 из bytes.Buffer
func ReadFloat(buf *bytes.Buffer) (float32, error) {
	if buf.Len() < 4 {
		return 0, fmt.Errorf("insufficient data for float: need 4, have %d", buf.Len())
	}

	var value float32
	if err := binary.Read(buf, binary.BigEndian, &value); err != nil {
		return 0, fmt.Errorf("failed to read float: %w", err)
	}
	return value, nil
}

// WriteFloat записывает float32 как 4 байта (big endian)
func WriteFloat(value float32) []byte {
	buf := make([]byte, 4)
	bits := math.Float32bits(value)
	binary.BigEndian.PutUint32(buf, bits)
	return buf
}

// ReadBool читает булево значение из bytes.Buffer
func ReadBool(buf *bytes.Buffer) (bool, error) {
	if buf.Len() < 1 {
		return false, fmt.Errorf("insufficient data for bool: need 1, have %d", buf.Len())
	}

	b, err := buf.ReadByte()
	if err != nil {
		return false, fmt.Errorf("failed to read bool: %w", err)
	}
	return b != 0, nil
}

// WriteBool записывает булево значение как 1 байт
func WriteBool(value bool) []byte {
	if value {
		return []byte{1}
	}
	return []byte{0}
}

// ReadShort читает int16 из bytes.Buffer
func ReadShort(buf *bytes.Buffer) (int16, error) {
	if buf.Len() < 2 {
		return 0, fmt.Errorf("insufficient data for short: need 2, have %d", buf.Len())
	}

	var value int16
	if err := binary.Read(buf, binary.BigEndian, &value); err != nil {
		return 0, fmt.Errorf("failed to read short: %w", err)
	}
	return value, nil
}

// WriteShort записывает int16 как 2 байта (big endian)
func WriteShort(value int16) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(value))
	return buf
}

// ReadInt читает int32 из bytes.Buffer
func ReadInt(buf *bytes.Buffer) (int32, error) {
	if buf.Len() < 4 {
		return 0, fmt.Errorf("insufficient data for int: need 4, have %d", buf.Len())
	}

	var value int32
	if err := binary.Read(buf, binary.BigEndian, &value); err != nil {
		return 0, fmt.Errorf("failed to read int: %w", err)
	}
	return value, nil
}

// WriteInt записывает int32 как 4 байта (big endian)
func WriteInt(value int32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(value))
	return buf
}

// ReadByte читает один байт из bytes.Buffer
func ReadByte(buf *bytes.Buffer) (byte, error) {
	if buf.Len() < 1 {
		return 0, fmt.Errorf("insufficient data for byte: need 1, have %d", buf.Len())
	}
	return buf.ReadByte()
}

// ReadFull читает точное количество байт из io.Reader
func ReadFull(r io.Reader, length int) ([]byte, error) {
	if length < 0 {
		return nil, errors.New("negative read length")
	}

	data := make([]byte, length)
	_, err := io.ReadFull(r, data)
	if err != nil {
		return nil, fmt.Errorf("failed to read %d bytes: %w", length, err)
	}
	return data, nil
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

// ReadByteFromBuf читает один байт из bytes.Buffer
func ReadByteFromBuf(buf *bytes.Buffer) (byte, error) {
	if buf.Len() < 1 {
		return 0, fmt.Errorf("insufficient data for byte: need 1, have %d", buf.Len())
	}
	return buf.ReadByte()
}

func WriteVarInt32ToBuffer(buf *bytes.Buffer, value int32) {
	u := uint32(value)
	for {
		b := byte(u & SegmentBits)
		u >>= 7
		if u != 0 {
			b |= ContinueBit
		}
		buf.WriteByte(b)
		if u == 0 {
			break
		}
	}
}

func WriteVarInt64ToBuffer(buf *bytes.Buffer, value int64) {
	u := uint64(value)
	for {
		b := byte(u & SegmentBits)
		u >>= 7
		if u != 0 {
			b |= ContinueBit
		}
		buf.WriteByte(b)
		if u == 0 {
			break
		}
	}
}
