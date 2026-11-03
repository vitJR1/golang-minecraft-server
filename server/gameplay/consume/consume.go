// Package consume is items that take time to use: what is eaten or drunk,
// for how long, with which sound, and the "using an item" pose other players
// see. The server tracks each use in progress and applies the result
// (server/consume.go).
package consume

import (
	"bytes"

	"minecraft-server/protocol"
)

// Ticks is the vanilla eat/drink duration (1.6 s).
const Ticks = 32

// Living-entity metadata index 8 "hand states": 0x01 = a hand is active
// (using an item), 0x02 = it's the off hand.
const (
	handStateIndex byte = 8
	// HandActive is the hand-state flag of a hand in use.
	HandActive byte = 0x01
)

// Consumable reports whether item is eaten/drunk over time.
func Consumable(item string) bool {
	return item == "minecraft:golden_apple" || item == "minecraft:potion"
}

// Sound is the sound of finishing item.
func Sound(item string) string {
	if item == "minecraft:potion" {
		return "minecraft:entity.generic.drink"
	}
	return "minecraft:entity.player.burp"
}

// HandStatePayload builds Set Entity Metadata with just the hand-state byte.
func HandStatePayload(eid int32, flags byte) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, eid)
	buf.WriteByte(handStateIndex)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // type: Byte
	buf.WriteByte(flags)
	buf.WriteByte(0xFF)
	return buf.Bytes()
}
