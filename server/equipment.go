package server

import (
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/equipment"
)

// equipment.go: Set Equipment (Cb 0x55, payload: server/equipment) — what
// other players see in a player's hands and on their body.
//
// It is broadcast to everyone else in the instance whenever something that
// shows changes — the selected hotbar slot, the held stack, armor — via
// equipmentChanged, and sent for every present player to a joining or
// resyncing client next to Spawn Player (sendOthersEquipment).

// equipmentPayload is this player's Set Equipment (equipment.Payload).
func (c *ClientConnection) equipmentPayload() []byte {
	return equipment.Payload(c.player.EntityID, &c.inv, c.heldSlot.Load())
}

// equipmentChanged tells everyone else in the instance what this player now
// holds and wears. Call after any change to the held slot, the held stack,
// armor or the off hand. Safe before login / outside an instance (no-op).
func (c *ClientConnection) equipmentChanged() {
	if c.player == nil || c.instance == nil {
		return
	}
	c.instance.Players.Broadcast(packetid.CbPlaySetEquipment, c.equipmentPayload(), c.player.EntityID)
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
		_ = c.safeWrite(packetid.CbPlaySetEquipment, other.equipmentPayload())
	}
}
