// Package equipment is Set Equipment (Cb 0x55): what other players see in a
// player's hands and on their body, built from the server-side inventory
// model (window-0 slots: 5 helmet, 6 chestplate, 7 leggings, 8 boots, 45 off
// hand, 36+held main hand) through inventory.WriteSlot, so NBT (enchant
// glint, dyed leather) travels along.
package equipment

import (
	"bytes"

	"minecraft-server/protocol"
	"minecraft-server/server/gameplay/inventory"
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

// Set is what a player shows: the stack in each equipment slot, indexed by
// the slot id (main hand 0 … helmet 5).
type Set [6]inventory.Stack

// Of is the equipment of inventory inv with hotbar slot heldSlot selected.
func Of(inv *inventory.Player, heldSlot int32) Set {
	var s Set
	s[mainHand] = inv.Held(heldSlot)
	s[offHand] = inv.Get(inventory.OffhandSlot)
	s[boots] = inv.Get(inventory.BootsSlot)
	s[leggings] = inv.Get(inventory.LeggingsSlot)
	s[chestplate] = inv.Get(inventory.ChestplateSlot)
	s[helmet] = inv.Get(inventory.HelmetSlot)
	return s
}

// Payload encodes all six equipment slots of the player with entity id eid,
// inventory inv and hotbar slot heldSlot selected. Sending every slot means
// a cleared slot is cleared on the client too — what a viewer that knows
// nothing yet (a joiner) needs.
func Payload(eid int32, inv *inventory.Player, heldSlot int32) []byte {
	return Of(inv, heldSlot).Payload(eid)
}

// Payload encodes every slot of s.
func (s Set) Payload(eid int32) []byte { return s.encode(eid, func(int) bool { return true }) }

// Diff encodes only the slots where s differs from was (what the viewers
// were last shown, a cleared slot included), and reports false when nothing
// differs. Vanilla sends just the changed slots too.
func (s Set) Diff(eid int32, was Set) ([]byte, bool) {
	changed := func(i int) bool { return !inventory.Equal(s[i], was[i]) }
	any := false
	for i := range s {
		any = any || changed(i)
	}
	if !any {
		return nil, false
	}
	return s.encode(eid, changed), true
}

// encode writes entity id, then (slot byte | 0x80 while more follow, Slot)
// for every slot keep selects.
func (s Set) encode(eid int32, keep func(int) bool) []byte {
	var slots []int
	for i := range s {
		if keep(i) {
			slots = append(slots, i)
		}
	}
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, eid)
	for n, i := range slots {
		slot := byte(i)
		if n < len(slots)-1 {
			slot |= 0x80 // another entry follows
		}
		buf.WriteByte(slot)
		inventory.WriteSlot(&buf, s[i])
	}
	return buf.Bytes()
}
