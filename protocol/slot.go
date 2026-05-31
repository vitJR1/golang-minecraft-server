package protocol

import (
	"minecraft-server/nbt"
)

// Slot encoding for Minecraft Java 1.20.1.
//
// Wire format:
//   Bool Present
//   If Present:
//     VarInt ItemID  (numeric ID from the items registry)
//     Byte   Count
//     NBT    (TAG_End single 0x00 byte when no NBT)
//
// An empty (absent) slot is one bool byte 0x00 — nothing else.

// WriteEmptySlot returns a single byte (0x00) for an empty inventory slot.
func WriteEmptySlot() []byte {
	return []byte{0x00}
}

// WriteSlot encodes a present item with no NBT (no custom name, no
// enchantments). Most icon items in our menus use this.
func WriteSlot(itemID int32, count byte) []byte {
	out := make([]byte, 0, 8)
	out = append(out, 0x01) // present = true
	out = append(out, WriteVarInt32(itemID)...)
	out = append(out, count)
	out = append(out, 0x00) // NBT TAG_End — no NBT compound
	return out
}

// WriteSlotWithName encodes a present item carrying a custom display name
// (the "yellow italic" label shown when the player hovers the slot). The
// name is wrapped in a JSON chat component before encoding, matching how
// the vanilla client interprets `display.Name`.
func WriteSlotWithName(itemID int32, count byte, displayName string) []byte {
	// Build NBT: {display: {Name: '{"text":"<displayName>"}'}}
	root := nbt.Compound{
		"display": nbt.Compound{
			"Name": nbt.String(`{"text":"` + escapeJSON(displayName) + `"}`),
		},
	}
	nbtBytes := nbt.Marshal(root)

	out := make([]byte, 0, 16+len(nbtBytes))
	out = append(out, 0x01) // present = true
	out = append(out, WriteVarInt32(itemID)...)
	out = append(out, count)
	out = append(out, nbtBytes...)
	return out
}

// escapeJSON escapes the minimum set of characters that would otherwise
// break a JSON string literal. Sufficient for our menu labels (ASCII +
// occasional double-quote); not a general-purpose JSON encoder.
func escapeJSON(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			out = append(out, '\\', s[i])
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
