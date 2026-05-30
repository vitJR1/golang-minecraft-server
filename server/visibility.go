package server

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

// playerInfoAddPayload builds the Player Info Update payload for "Add
// Player" + "Update Listed". Use this both to bootstrap a newcomer's tab
// list (with everybody currently online) and to announce a single newcomer
// to existing players.
func playerInfoAddPayload(conns []*ClientConnection) []byte {
	if len(conns) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteByte(playerInfoActionAdd | playerInfoActionListed)
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(conns)))
	for _, c := range conns {
		p := c.player
		buf.Write(p.UUID[:])
		// Add Player data: name + empty properties array.
		buf.Write(protocol.WriteString(p.Name))
		protocol.WriteVarInt32ToBuffer(&buf, 0)
		// Update Listed data: show in tab list.
		buf.WriteByte(1)
	}
	return buf.Bytes()
}

// playerInfoRemovePayload signals removal of one or more players from the
// tab list. Use alongside Remove Entities to fully despawn.
func playerInfoRemovePayload(uuids [][16]byte) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(uuids)))
	for _, u := range uuids {
		buf.Write(u[:])
	}
	return buf.Bytes()
}

// spawnPlayerPayload builds the Spawn Player packet body. Yaw/Pitch are
// quantized to a single byte each (256-step) — the angle precision the
// client uses for entity rendering. Reads mutable state via Snapshot so a
// concurrent MoveTo from the owning goroutine can't tear the position.
func spawnPlayerPayload(p *player.Player) []byte {
	s := p.Snapshot()
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

// removeEntitiesPayload despawns one or more entities by ID. For player
// disconnects, send Player Info Remove first so the tab list stays clean.
func removeEntitiesPayload(ids []int32) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(ids)))
	for _, id := range ids {
		protocol.WriteVarInt32ToBuffer(&buf, id)
	}
	return buf.Bytes()
}

// broadcastEntityTeleport notifies every other player IN THE SAME INSTANCE
// about this player's current position. Called from handlePlay position
// handlers — once c.player has been updated via MoveTo/MoveAndLook/LookAt.
func (c *ClientConnection) broadcastEntityTeleport() {
	c.instance.Players.Broadcast(
		CbPlayTeleportEntity,
		teleportEntityPayload(c.player),
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
	c.instance.Players.Broadcast(CbPlayEntityAnimation, buf.Bytes(), c.player.EntityID)
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
	c.instance.Players.Broadcast(CbPlayHeadRotation, buf.Bytes(), s.EntityID)
}

// teleportEntityPayload sends an absolute-coordinate entity position. Used
// for all player movement updates for now — simpler than computing relative
// short deltas and stays correct for any move size.
func teleportEntityPayload(p *player.Player) []byte {
	s := p.Snapshot()
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
