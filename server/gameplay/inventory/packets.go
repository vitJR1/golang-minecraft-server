package inventory

import (
	"bytes"

	"minecraft-server/protocol"
)

// ContentsPayload builds Set Container Content: the window's slots in order,
// then the carried (cursor) item. The state id is always 0 — the server is
// authoritative, the client's state ids aren't checked.
func ContentsPayload(window byte, slots []Stack, cursor Stack) []byte {
	var buf bytes.Buffer
	buf.WriteByte(window)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(slots)))
	for _, st := range slots {
		WriteSlot(&buf, st)
	}
	WriteSlot(&buf, cursor)
	return buf.Bytes()
}

// SlotPayload builds Set Container Slot for one slot of a window (window 0
// is the player's own inventory; slot a window index).
func SlotPayload(window byte, slot int16, st Stack) []byte {
	var buf bytes.Buffer
	buf.WriteByte(window)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	buf.Write(protocol.WriteShort(slot))
	WriteSlot(&buf, st) // keeps name / lore / enchantments / damage
	return buf.Bytes()
}
