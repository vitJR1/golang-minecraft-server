// Package sound builds the Sound Effect packet (a named sound at a world
// position) and holds the sound categories and audible range the server
// uses (Instance.playSound broadcasts it).
package sound

import (
	"bytes"
	"minecraft-server/protocol"
)

// Sound categories (the VarInt "Sound Source" enum in Sound Effect).
const (
	CategoryBlocks = 4
	CategoryPlayer = 7
)

// BaseRange is how far a volume-1 sound carries before the client
// attenuates it to silence; louder sounds scale it (vanilla: 16 * volume).
const BaseRange = 16.0

// EffectPayload builds Sound Effect (0x62) for a named sound played at
// a world position. We use the inline-name form (Sound ID VarInt 0 + name)
// so we don't depend on the numeric sound registry, which shifts between
// versions. Position is fixed-point (block coord × 8). Seed is 0.
func EffectPayload(name string, category int32, x, y, z float64, volume, pitch float32) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, 0) // 0 → inline SoundEvent follows
	buf.Write(protocol.WriteString(name))
	buf.WriteByte(0) // has fixed range = false
	protocol.WriteVarInt32ToBuffer(&buf, category)
	buf.Write(protocol.WriteInt(int32(x * 8)))
	buf.Write(protocol.WriteInt(int32(y * 8)))
	buf.Write(protocol.WriteInt(int32(z * 8)))
	buf.Write(protocol.WriteFloat(volume))
	buf.Write(protocol.WriteFloat(pitch))
	buf.Write(protocol.WriteLong(0)) // seed
	return buf.Bytes()
}
