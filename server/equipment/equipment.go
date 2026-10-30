// Package equipment is Set Equipment (Cb 0x55): what other players see in a
// player's hands and on their body, built from the server-side inventory
// model (window-0 slots: 5 helmet, 6 chestplate, 7 leggings, 8 boots, 45 off
// hand, 36+held main hand) through inventory.WriteSlot, so NBT (enchant
// glint, dyed leather) travels along.
package equipment

import (
	"bytes"

	"minecraft-server/protocol"
	"minecraft-server/server/inventory"
)

// Equipment slot ids of the Set Equipment packet.
const (
	mainHand   byte = 0
	offHand    byte = 1
	boots      byte = 2
	leggings   byte = 3
	chestplate byte = 4
	helmet     byte = 5
)

// Payload encodes all six equipment slots of the player with entity id eid,
// inventory inv and hotbar slot heldSlot selected: entity id, then (slot
// byte | 0x80 while more follow, Slot) per entry. Sending every slot each
// time means a cleared slot is cleared on the client too.
func Payload(eid int32, inv *inventory.Player, heldSlot int32) []byte {
	entries := []struct {
		slot byte
		st   inventory.Stack
	}{
		{mainHand, inv.Held(heldSlot)},
		{offHand, inv.Get(inventory.OffhandSlot)},
		{boots, inv.Get(inventory.BootsSlot)},
		{leggings, inv.Get(inventory.LeggingsSlot)},
		{chestplate, inv.Get(inventory.ChestplateSlot)},
		{helmet, inv.Get(inventory.HelmetSlot)},
	}
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, eid)
	for i, e := range entries {
		slot := e.slot
		if i < len(entries)-1 {
			slot |= 0x80 // another entry follows
		}
		buf.WriteByte(slot)
		inventory.WriteSlot(&buf, e.st)
	}
	return buf.Bytes()
}
