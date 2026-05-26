package server

import (
	"bytes"
	"fmt"
	"minecraft-server/utils"
)

// readXYZ parses three big-endian doubles.
func readXYZ(buf *bytes.Buffer) (x, y, z float64, err error) {
	if x, err = utils.ReadDouble(buf); err != nil {
		return 0, 0, 0, fmt.Errorf("X: %w", err)
	}
	if y, err = utils.ReadDouble(buf); err != nil {
		return 0, 0, 0, fmt.Errorf("Y: %w", err)
	}
	if z, err = utils.ReadDouble(buf); err != nil {
		return 0, 0, 0, fmt.Errorf("Z: %w", err)
	}
	return x, y, z, nil
}

// readYawPitchOnGround parses two floats and a bool — the trailing fields of
// Set Player Rotation and Set Player Position and Rotation.
func readYawPitchOnGround(buf *bytes.Buffer) (yaw, pitch float32, onGround bool, err error) {
	if yaw, err = utils.ReadFloat(buf); err != nil {
		return 0, 0, false, fmt.Errorf("yaw: %w", err)
	}
	if pitch, err = utils.ReadFloat(buf); err != nil {
		return 0, 0, false, fmt.Errorf("pitch: %w", err)
	}
	if onGround, err = utils.ReadBool(buf); err != nil {
		return 0, 0, false, fmt.Errorf("onGround: %w", err)
	}
	return yaw, pitch, onGround, nil
}

// readPosOnGround parses Set Player Position (X Y Z OnGround).
func readPosOnGround(buf *bytes.Buffer) (x, y, z float64, onGround bool, err error) {
	if x, y, z, err = readXYZ(buf); err != nil {
		return 0, 0, 0, false, err
	}
	if onGround, err = utils.ReadBool(buf); err != nil {
		return 0, 0, 0, false, fmt.Errorf("onGround: %w", err)
	}
	return x, y, z, onGround, nil
}
