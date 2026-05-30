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

// announceJoin makes a newly logged-in player visible to everyone, and
// makes everyone else visible to the newcomer. Must run AFTER c is in
// Server.Players (so the newcomer's tab-list bootstrap includes self).
//
// Protocol ordering matters: Player Info Update must precede Spawn Player,
// otherwise the client rejects the spawn for an unknown UUID.
// joinAndAnnounce atomically registers a newly-logged-in player and sends
// the visibility packets. Holding s.joinMu while running both Add and the
// announce eliminates the race where one player's announceJoin snapshot
// could include another player who hasn't been announced yet.
func (s *Server) joinAndAnnounce(c *ClientConnection) {
	s.joinMu.Lock()
	defer s.joinMu.Unlock()

	s.Players.Add(c)

	others := s.Players.snapshot()

	// 1. Newcomer gets the full tab list (everyone, including self).
	if payload := playerInfoAddPayload(others); payload != nil {
		_ = c.safeWrite(CbPlayPlayerInfoUpdate, payload)
	}

	// 2. Newcomer spawns the visible entity for every other player.
	for _, other := range others {
		if other == c {
			continue
		}
		_ = c.safeWrite(CbPlaySpawnPlayer, spawnPlayerPayload(other.player))
	}

	// 3. Everyone else adds newcomer to their tab list + spawns the entity.
	addNewcomer := playerInfoAddPayload([]*ClientConnection{c})
	spawnNewcomer := spawnPlayerPayload(c.player)
	s.Players.Broadcast(CbPlayPlayerInfoUpdate, addNewcomer, c.player.EntityID)
	s.Players.Broadcast(CbPlaySpawnPlayer, spawnNewcomer, c.player.EntityID)
}

// leaveAndAnnounce removes a departing player from everyone else's view and
// unregisters from PlayerList — under the same joinMu the join path uses,
// so concurrent join/leave traffic stays consistent. Safe to call when
// c.player is nil (early disconnect before login completed): no-ops.
func (s *Server) leaveAndAnnounce(c *ClientConnection) {
	if c.player == nil {
		return
	}
	s.joinMu.Lock()
	defer s.joinMu.Unlock()

	// Broadcast first (player still in list so except-filter works), then
	// remove. Mirror the join order in reverse: despawn, then tab-list.
	s.Players.Broadcast(
		CbPlayRemoveEntities,
		removeEntitiesPayload([]int32{c.player.EntityID}),
		c.player.EntityID,
	)
	s.Players.Broadcast(
		CbPlayPlayerInfoRemove,
		playerInfoRemovePayload([][16]byte{c.player.UUID}),
		c.player.EntityID,
	)
	s.Players.Remove(c.player.EntityID)
}

// broadcastEntityTeleport notifies every other player about this player's
// current position. Called from handlePlay position handlers — once
// c.player has been updated via MoveTo/MoveAndLook/LookAt.
func (c *ClientConnection) broadcastEntityTeleport() {
	c.server.Players.Broadcast(
		CbPlayTeleportEntity,
		teleportEntityPayload(c.player),
		c.player.EntityID,
	)
}

// broadcastEntityAnimation tells every other player to play an animation
// on this player's entity. anim is the vanilla animation enum:
// 0=swing main, 2=leave bed, 3=swing off-hand, 4=crit, 5=magic crit.
func (c *ClientConnection) broadcastEntityAnimation(anim byte) {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, c.player.EntityID)
	buf.WriteByte(anim)
	c.server.Players.Broadcast(CbPlayEntityAnimation, buf.Bytes(), c.player.EntityID)
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
