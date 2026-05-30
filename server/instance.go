package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"sync"
)

// Instance is one game-world "room": its own block storage, its own
// player list, its own broadcast scope. The Hub instance is just an
// instance like any other. Cross-instance teleport (Server.MovePlayer)
// is a later step.
//
// All visibility-affecting operations live on Instance: chat broadcasts,
// block updates, player join/leave announcements. Server stays the
// coordinator (entity-ID allocator, ops, listener of TCP).
type Instance struct {
	ID      string
	Server  *Server
	World   world.World
	Players *PlayerList

	// joinMu serializes registration + visibility announcements per
	// instance, so one player's join can't observe another mid-join inside
	// the same instance.
	joinMu sync.Mutex
}

// NewInstance creates an empty instance. The caller registers it on the
// server (currently just by assigning to Server.Hub).
func NewInstance(id string, srv *Server, w world.World) *Instance {
	return &Instance{
		ID:      id,
		Server:  srv,
		World:   w,
		Players: NewPlayerList(),
	}
}

// JoinAndAnnounce registers c in this instance's player list and sends the
// visibility packets that make c and the existing players mutually
// visible. Run AFTER c.player and c.instance are set.
func (i *Instance) JoinAndAnnounce(c *ClientConnection) {
	i.joinMu.Lock()
	defer i.joinMu.Unlock()

	i.Players.Add(c)

	others := i.Players.snapshot()

	// 1. Newcomer gets the full tab list (everyone in THIS instance).
	if payload := playerInfoAddPayload(others); payload != nil {
		_ = c.safeWrite(CbPlayPlayerInfoUpdate, payload)
	}

	// 2. Newcomer spawns the visible entity for every other player here.
	for _, other := range others {
		if other == c {
			continue
		}
		_ = c.safeWrite(CbPlaySpawnPlayer, spawnPlayerPayload(other.player))
	}

	// 3. Everyone else in the instance learns about the newcomer.
	addNewcomer := playerInfoAddPayload([]*ClientConnection{c})
	spawnNewcomer := spawnPlayerPayload(c.player)
	i.Players.Broadcast(CbPlayPlayerInfoUpdate, addNewcomer, c.player.EntityID)
	i.Players.Broadcast(CbPlaySpawnPlayer, spawnNewcomer, c.player.EntityID)
}

// LeaveAndAnnounce removes the player from the instance and tells the
// remaining players to despawn them. No-op when c.player is nil (early
// disconnect before login completed).
func (i *Instance) LeaveAndAnnounce(c *ClientConnection) {
	if c.player == nil {
		return
	}
	i.joinMu.Lock()
	defer i.joinMu.Unlock()

	i.Players.Broadcast(
		CbPlayRemoveEntities,
		removeEntitiesPayload([]int32{c.player.EntityID}),
		c.player.EntityID,
	)
	i.Players.Broadcast(
		CbPlayPlayerInfoRemove,
		playerInfoRemovePayload([][16]byte{c.player.UUID}),
		c.player.EntityID,
	)
	i.Players.Remove(c.player.EntityID)
}

// SetBlock updates this instance's world and broadcasts a Block Update to
// every player here. Players in other instances see nothing.
func (i *Instance) SetBlock(p world.Position, b world.Block) {
	i.World.SetBlock(p, b)

	var buf bytes.Buffer
	buf.Write(protocol.WritePosition(p.X, p.Y, p.Z))
	protocol.WriteVarInt32ToBuffer(&buf, b.StateID)
	i.Players.Broadcast(CbPlayBlockUpdate, buf.Bytes(), -1)
}

// BroadcastChat sends a chat line to every player in this instance. Format
// is "<sender> message"; an empty sender renders as a plain server line.
func (i *Instance) BroadcastChat(sender, message string) {
	var line string
	if sender == "" {
		line = message
	} else {
		line = fmt.Sprintf("<%s> %s", sender, message)
	}
	i.Players.Broadcast(CbPlaySystemChat, buildSystemChatPayload(line), -1)
}

// buildSystemChatPayload assembles a System Chat (Cb 0x64) body: JSON chat
// component string + overlay bool. encoding/json handles escaping so we
// can pass arbitrary text without breaking the wire format.
func buildSystemChatPayload(text string) []byte {
	encoded, _ := json.Marshal(map[string]string{"text": text})
	return append(protocol.WriteString(string(encoded)), 0)
}
