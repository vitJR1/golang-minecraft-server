package server

import (
	"bytes"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/visibility"
)

// tabEntries is conns as tab-list entries.
func tabEntries(conns []*ClientConnection) []visibility.Entry {
	out := make([]visibility.Entry, 0, len(conns))
	for _, c := range conns {
		out = append(out, visibility.Entry{UUID: c.player.UUID, Name: c.player.Name})
	}
	return out
}

// broadcastEntityTeleport notifies every other player IN THE SAME INSTANCE
// about this player's current position. Called from handlePlay position
// handlers — once c.player has been updated via MoveTo/MoveAndLook/LookAt.
func (c *ClientConnection) broadcastEntityTeleport() {
	c.instance.Players.Broadcast(
		packetid.CbPlayTeleportEntity,
		visibility.TeleportPayload(c.player.Snapshot()),
		c.player.EntityID,
	)
}

// broadcastEntityAnimation tells every other player in this instance to
// play an animation on this player's entity. anim is the vanilla
// animation enum: 0=swing main, 2=leave bed, 3=swing off-hand, 4=crit, 5=magic crit.
func (c *ClientConnection) broadcastEntityAnimation(anim byte) {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, c.player.EntityID)
	buf.WriteByte(anim)
	c.instance.Players.Broadcast(packetid.CbPlayEntityAnimation, buf.Bytes(), c.player.EntityID)
}

// broadcastHeadRotation syncs head yaw separately from body yaw. Vanilla
// tracks them independently: Teleport Entity / Update Entity Rotation only
// updates the body. Without this, other clients see the player's head
// stuck at its initial direction even as the body rotates (the "facing
// away from you" bug).
func (c *ClientConnection) broadcastHeadRotation() {
	s := c.player.Snapshot()
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, s.EntityID)
	buf.WriteByte(protocol.AngleToByte(s.Yaw))
	c.instance.Players.Broadcast(packetid.CbPlayHeadRotation, buf.Bytes(), s.EntityID)
}
