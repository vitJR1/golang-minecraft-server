package utils

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
)

func ReadPacket(conn net.Conn) (*bytes.Buffer, error) {
	length, err := ReadVarInt(conn)
	if err != nil {
		return nil, err
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}
	return bytes.NewBuffer(data), nil
}

func ReadPacket2(conn net.Conn) (packetID int, data []byte, err error) {
	// читаем длину пакета (VarInt)
	length, err := ReadVarInt(conn)
	if err != nil {
		return 0, nil, err
	}

	packetData := make([]byte, length)
	if _, err := io.ReadFull(conn, packetData); err != nil {
		return 0, nil, err
	}

	// первый VarInt — ID пакета
	packetID, n := ReadVarIntFromBytes(packetData)
	if n == 0 {
		return 0, nil, fmt.Errorf("failed to read packet ID")
	}

	// возвращаем ID и оставшиеся данные
	return packetID, packetData[n:], nil
}

func WritePacket(conn net.Conn, packetID int32, payload []byte) error {
	id := WriteVarInt32(packetID)
	body := append(id, payload...)
	length := WriteVarInt32(int32(len(body)))
	_, err := conn.Write(append(length, body...))
	return err
}

func ReadVarInt(r io.Reader) (int, error) {
	var num int
	var shift uint
	for {
		var b [1]byte
		if _, err := r.Read(b[:]); err != nil {
			return 0, err
		}
		num |= int(b[0]&0x7F) << shift
		if b[0]&0x80 == 0 {
			break
		}
		shift += 7
	}
	return num, nil
}

func WriteVarInt32(value int32) []byte {
	var out []byte

	for i := 0; i < 5; i++ {
		if (value & ^0x7F) == 0 {
			out = append(out, byte(value))
			return out
		}

		out = append(out, byte(value&0x7F|0x80))
		value >>= 7
	}

	panic("VarInt32 too big")
}

func ReadString(r io.Reader) (string, error) {
	length, err := ReadVarInt(r)
	if err != nil {
		return "", err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func ReadUShort(r io.Reader) (uint16, error) {
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return uint16(buf[0])<<8 | uint16(buf[1]), nil
}

func WriteString(s string) []byte {
	data := []byte(s)
	length := WriteVarInt32(int32(len(data)))
	return append(length, data...)
}

func ReadVarIntFromBytes(data []byte) (value int, bytesRead int) {
	var numRead int
	var result int
	for {
		if numRead >= len(data) {
			return 0, 0 // недостаточно данных
		}
		b := data[numRead]
		valuePart := int(b & 0x7F)
		result |= valuePart << (7 * numRead)
		numRead++
		if (b & 0x80) == 0 {
			break
		}
		if numRead > 5 {
			return 0, 0 // VarInt слишком длинный
		}
	}
	return result, numRead
}

func ReadByteArray(data []byte) (out []byte, rest []byte, err error) {
	length, n := ReadVarIntFromBytes(data)
	if n == 0 {
		return nil, nil, fmt.Errorf("failed to read VarInt length")
	}

	if len(data) < n+length {
		return nil, nil, fmt.Errorf("not enough data: need %d, have %d", n+length, len(data))
	}

	out = data[n : n+length]
	rest = data[n+length:]
	return out, rest, nil
}

func WriteLong(value int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(value))
	return buf
}

func WriteDouble(value float64) []byte {
	bits := math.Float64bits(value)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, bits)
	return buf
}

func WriteFloat(value float32) []byte {
	bits := math.Float32bits(value)
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, bits)
	return buf
}
