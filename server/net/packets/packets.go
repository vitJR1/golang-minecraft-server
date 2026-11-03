// Package packets decodes the serverbound packets the connection handlers
// act on (protocol 763): the handshake, Login Start and the play packets
// with more than one field. Each decoder reads exactly what the handler
// used to read inline and is as strict: fields a handler ignored are read
// without failing on them, so a short packet passes or fails just as
// before. Acting on a packet stays in the server.
package packets

import (
	"bytes"
	"fmt"

	"minecraft-server/protocol"
)

// --- handshake / login ------------------------------------------------------

// Next states a Handshake may ask for.
const (
	NextStatus = 1
	NextLogin  = 2
)

// Handshake is the first packet: only the requested next state matters.
type Handshake struct {
	Protocol int
	Address  string
	Port     uint16
	Next     int
}

// ReadHandshake decodes a Handshake.
func ReadHandshake(buf *bytes.Buffer) (Handshake, error) {
	var h Handshake
	var err error
	if h.Protocol, err = protocol.ReadVarInt(buf); err != nil {
		return h, fmt.Errorf("reading protocol version: %w", err)
	}
	if h.Address, err = protocol.ReadStringFromBuf(buf); err != nil {
		return h, fmt.Errorf("reading server address: %w", err)
	}
	if h.Port, err = protocol.ReadUShortFromBuf(buf); err != nil {
		return h, fmt.Errorf("reading port: %w", err)
	}
	if h.Next, err = protocol.ReadVarInt(buf); err != nil {
		return h, fmt.Errorf("reading next state: %w", err)
	}
	return h, nil
}

// ReadLoginStart decodes Login Start: the player name (the UUID after it is
// ignored, like vanilla offline mode).
func ReadLoginStart(buf *bytes.Buffer) (string, error) {
	name, err := protocol.ReadStringFromBuf(buf)
	if err != nil {
		return "", fmt.Errorf("reading player name: %w", err)
	}
	return name, nil
}

// EncryptionResponse is the client's RSA-encrypted shared secret and verify
// token.
type EncryptionResponse struct {
	SharedSecret, VerifyToken []byte
}

// ReadEncryptionResponse decodes an Encryption Response body.
func ReadEncryptionResponse(buf *bytes.Buffer) (EncryptionResponse, error) {
	var r EncryptionResponse
	var err error
	if r.SharedSecret, err = protocol.ReadByteArrayFromBuf(buf); err != nil {
		return r, fmt.Errorf("reading shared secret: %w", err)
	}
	if r.VerifyToken, err = protocol.ReadByteArrayFromBuf(buf); err != nil {
		return r, fmt.Errorf("reading verify token: %w", err)
	}
	return r, nil
}

// --- movement -----------------------------------------------------------------

// Move is a Set Player Position / Position and Rotation / Rotation packet;
// HasPos / HasLook say which fields it carried.
type Move struct {
	X, Y, Z         float64
	Yaw, Pitch      float32
	OnGround        bool
	HasPos, HasLook bool
}

// ReadPos decodes Set Player Position (X Y Z OnGround).
func ReadPos(buf *bytes.Buffer) (Move, error) {
	m := Move{HasPos: true}
	var err error
	if m.X, m.Y, m.Z, err = readXYZ(buf); err != nil {
		return m, fmt.Errorf("set player position: %w", err)
	}
	if m.OnGround, err = protocol.ReadBool(buf); err != nil {
		return m, fmt.Errorf("set player position: onGround: %w", err)
	}
	return m, nil
}

// ReadPosRot decodes Set Player Position and Rotation.
func ReadPosRot(buf *bytes.Buffer) (Move, error) {
	m := Move{HasPos: true, HasLook: true}
	var err error
	if m.X, m.Y, m.Z, err = readXYZ(buf); err != nil {
		return m, fmt.Errorf("set player position+rotation: %w", err)
	}
	if m.Yaw, m.Pitch, m.OnGround, err = readYawPitchOnGround(buf); err != nil {
		return m, fmt.Errorf("set player position+rotation: %w", err)
	}
	return m, nil
}

// ReadRot decodes Set Player Rotation.
func ReadRot(buf *bytes.Buffer) (Move, error) {
	m := Move{HasLook: true}
	var err error
	if m.Yaw, m.Pitch, m.OnGround, err = readYawPitchOnGround(buf); err != nil {
		return m, fmt.Errorf("set player rotation: %w", err)
	}
	return m, nil
}

// readXYZ parses three big-endian doubles.
func readXYZ(buf *bytes.Buffer) (x, y, z float64, err error) {
	if x, err = protocol.ReadDouble(buf); err != nil {
		return 0, 0, 0, fmt.Errorf("X: %w", err)
	}
	if y, err = protocol.ReadDouble(buf); err != nil {
		return 0, 0, 0, fmt.Errorf("Y: %w", err)
	}
	if z, err = protocol.ReadDouble(buf); err != nil {
		return 0, 0, 0, fmt.Errorf("Z: %w", err)
	}
	return x, y, z, nil
}

// readYawPitchOnGround parses two floats and a bool — the trailing fields of
// Set Player Rotation and Set Player Position and Rotation.
func readYawPitchOnGround(buf *bytes.Buffer) (yaw, pitch float32, onGround bool, err error) {
	if yaw, err = protocol.ReadFloat(buf); err != nil {
		return 0, 0, false, fmt.Errorf("yaw: %w", err)
	}
	if pitch, err = protocol.ReadFloat(buf); err != nil {
		return 0, 0, false, fmt.Errorf("pitch: %w", err)
	}
	if onGround, err = protocol.ReadBool(buf); err != nil {
		return 0, 0, false, fmt.Errorf("onGround: %w", err)
	}
	return yaw, pitch, onGround, nil
}
