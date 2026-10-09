package server

import (
	"bytes"

	"minecraft-server/protocol"
)

// equipment.go: Set Equipment (Cb 0x55) — what other players see in a
// player's hands and on their body. The payload is built from the
// server-side inventory model (window-0 slots: 5 helmet, 6 chestplate,
// 7 leggings, 8 boots, 45 off hand, 36+heldSlot main hand) through
// writeStack, so NBT (enchant glint, dyed leather) travels along.
//
// It is broadcast to everyone else in the instance whenever something that
// shows changes — the selected hotbar slot, the held stack, armor — via
// equipmentChanged, and sent for every present player to a joining or
// resyncing client next to Spawn Player (sendOthersEquipment).

// Equipment slot ids of the Set Equipment packet.
const (
	equipMainHand   byte = 0
	equipOffHand    byte = 1
	equipBoots      byte = 2
	equipLeggings   byte = 3
	equipChestplate byte = 4
	equipHelmet     byte = 5
)

// Window-0 indices of the armor and off-hand slots (helmet is
// armorHelmetSlot in modifiers.go).
const (
	armorChestplateSlot int16 = 6
	armorLeggingsSlot   int16 = 7
	armorBootsSlot      int16 = 8
	offHandSlot         int16 = 45
)

// equipmentPayload encodes all six equipment slots of this player: entity
// id, then (slot byte | 0x80 while more follow, Slot) per entry. Sending
// every slot each time means a cleared slot is cleared on the client too.
func (c *ClientConnection) equipmentPayload() []byte {
	entries := []struct {
		slot byte
		st   itemStack
	}{
		{equipMainHand, c.inv.held(c.heldSlot.Load())},
		{equipOffHand, c.inv.get(offHandSlot)},
		{equipBoots, c.inv.get(armorBootsSlot)},
		{equipLeggings, c.inv.get(armorLeggingsSlot)},
		{equipChestplate, c.inv.get(armorChestplateSlot)},
		{equipHelmet, c.inv.get(armorHelmetSlot)},
	}
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, c.player.EntityID)
	for i, e := range entries {
		slot := e.slot
		if i < len(entries)-1 {
			slot |= 0x80 // another entry follows
		}
		buf.WriteByte(slot)
		writeStack(&buf, e.st)
	}
	return buf.Bytes()
}

// equipmentChanged tells everyone else in the instance what this player now
// holds and wears. Call after any change to the held slot, the held stack,
// armor or the off hand. Safe before login / outside an instance (no-op).
func (c *ClientConnection) equipmentChanged() {
	if c.player == nil || c.instance == nil {
		return
	}
	c.instance.Players.Broadcast(CbPlaySetEquipment, c.equipmentPayload(), c.player.EntityID)
}

// sendOthersEquipment sends this client the equipment of every other player
// in its instance — right after their Spawn Player on join / resync, since
// Spawn Player alone shows them empty-handed.
func (c *ClientConnection) sendOthersEquipment() {
	if c.instance == nil {
		return
	}
	for _, other := range c.instance.Players.snapshot() {
		if other == c || other.player == nil {
			continue
		}
		_ = c.safeWrite(CbPlaySetEquipment, other.equipmentPayload())
	}
}
