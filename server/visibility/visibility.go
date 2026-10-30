// Package visibility is the packets that make players visible to each other:
// the tab list (Player Info Update / Remove), the player entity (Spawn
// Player, Teleport Entity) and despawning entities (Remove Entities). The
// server decides who sees whom and sends them (server/visibility.go).
package visibility

import (
	"bytes"

	"minecraft-server/player"
	"minecraft-server/protocol"
)

// Bit values for the Player Info Update "Actions" mask. Per the 1.20.1 spec
// each player entry encodes data for each action set in the mask, in bit
// order from lowest to highest.
const (
	playerInfoActionAdd    = 0x01
	playerInfoActionListed = 0x08
)

// Entry is one player in the tab list.
type Entry struct {
	UUID [16]byte
	Name string
}

// InfoAddPayload builds the Player Info Update payload for "Add Player" +
// "Update Listed" (nil for no entries). Use it both to bootstrap a
// newcomer's tab list (with everybody currently online) and to announce a
// single newcomer to existing players.
func InfoAddPayload(entries []Entry) []byte {
	if len(entries) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteByte(playerInfoActionAdd | playerInfoActionListed)
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(entries)))
	for _, e := range entries {
		buf.Write(e.UUID[:])
		// Add Player data: name + empty properties array.
		buf.Write(protocol.WriteString(e.Name))
		protocol.WriteVarInt32ToBuffer(&buf, 0)
		// Update Listed data: show in tab list.
		buf.WriteByte(1)
	}
	return buf.Bytes()
}

// InfoRemovePayload takes players off the tab list. Use alongside Remove
// Entities to fully despawn.
func InfoRemovePayload(uuids [][16]byte) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(uuids)))
	for _, u := range uuids {
		buf.Write(u[:])
	}
	return buf.Bytes()
}

// SpawnPlayerPayload builds Spawn Player for a player as s shows it.
// Yaw/Pitch are quantized to a single byte each (256-step) — the angle
// precision the client uses for entity rendering.
func SpawnPlayerPayload(s player.Snapshot) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, s.EntityID)
	buf.Write(s.UUID[:])
	buf.Write(protocol.WriteDouble(s.X))
	buf.Write(protocol.WriteDouble(s.Y))
	buf.Write(protocol.WriteDouble(s.Z))
	buf.WriteByte(protocol.AngleToByte(s.Yaw))
	buf.WriteByte(protocol.AngleToByte(s.Pitch))
	return buf.Bytes()
}

// RemoveEntitiesPayload despawns one or more entities by id. For player
// disconnects, send InfoRemovePayload first so the tab list stays clean.
func RemoveEntitiesPayload(ids []int32) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(ids)))
	for _, id := range ids {
		protocol.WriteVarInt32ToBuffer(&buf, id)
	}
	return buf.Bytes()
}

// TeleportPayload builds Teleport Entity: a player's absolute position and
// look as s shows it. Used for all player movement updates — simpler than
// relative short deltas and correct for any move size.
func TeleportPayload(s player.Snapshot) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, s.EntityID)
	buf.Write(protocol.WriteDouble(s.X))
	buf.Write(protocol.WriteDouble(s.Y))
	buf.Write(protocol.WriteDouble(s.Z))
	buf.WriteByte(protocol.AngleToByte(s.Yaw))
	buf.WriteByte(protocol.AngleToByte(s.Pitch))
	if s.OnGround {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}
