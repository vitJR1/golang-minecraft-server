package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"sync"
	"sync/atomic"
	"time"
)

// TickRate is the per-instance tick frequency in Hz. Matches vanilla.
const TickRate = 20

// tickInterval is the wall-clock duration between ticks.
var tickInterval = time.Second / TickRate

// TickHandler fires once per tick. tick is the monotonically-increasing
// counter since the instance started. Handlers should be fast — if any
// handler takes longer than tickInterval, the next tick is dropped (the
// ticker doesn't queue), so a slow handler stretches game time visibly.
type TickHandler func(tick uint64)

// Instance is one game-world "room": its own block storage, its own
// player list, its own tick loop, its own broadcast scope. The Hub
// instance is just an instance like any other. Cross-instance teleport
// (Server.MovePlayer) is a later step.
//
// All visibility-affecting operations live on Instance: chat broadcasts,
// block updates, player join/leave announcements. Server stays the
// coordinator (entity-ID allocator, ops, listener of TCP).
//
// Event hooks (OnPlayerJoin/Leave/etc.) are the integration point for
// game logic. Set them at instance construction; the core server calls
// them at well-defined points and respects their return values (veto for
// block events, rewrite/veto for chat). nil hook = default (allow + no-op).
type Instance struct {
	ID      string
	Server  *Server
	World   world.World
	Players *PlayerList

	// joinMu serializes registration + visibility announcements per
	// instance, so one player's join can't observe another mid-join inside
	// the same instance.
	joinMu sync.Mutex

	// Tick loop state.
	tickCount    atomic.Uint64
	tickMu       sync.RWMutex
	tickHandlers []TickHandler
	stopTick     chan struct{} // closed by Stop()
	stopOnce     sync.Once

	// Event hooks. Set at construction, read from handler goroutines.
	// Don't reassign after the instance starts taking traffic — there's no
	// synchronization on these fields.
	OnPlayerJoin  func(c *ClientConnection)
	OnPlayerLeave func(c *ClientConnection)
	// OnBlockBreak / OnBlockPlace return true to allow the action. False
	// causes the server to send a corrective Block Update with the prior
	// state so the client rolls back its prediction.
	OnBlockBreak func(c *ClientConnection, pos world.Position) bool
	OnBlockPlace func(c *ClientConnection, pos world.Position, block world.Block) bool
	// OnChat may rewrite the message text and/or veto delivery. The first
	// return is the (possibly modified) text to broadcast; the second is
	// false to drop the message entirely.
	OnChat func(c *ClientConnection, msg string) (rewrite string, allow bool)
}

// NewInstance creates an instance and starts its tick loop. Caller
// registers it on the server (currently just by assigning to Server.Hub
// or by future matchmaker logic).
func NewInstance(id string, srv *Server, w world.World) *Instance {
	i := &Instance{
		ID:       id,
		Server:   srv,
		World:    w,
		Players:  NewPlayerList(),
		stopTick: make(chan struct{}),
	}
	go i.tickLoop()
	return i
}

// OnTick registers a callback fired once per tick (20 Hz). Concurrent-safe.
// There is no Unsubscribe yet — destroy the whole instance via Stop when
// you want everything to go away.
func (i *Instance) OnTick(h TickHandler) {
	i.tickMu.Lock()
	i.tickHandlers = append(i.tickHandlers, h)
	i.tickMu.Unlock()
}

// Tick returns the current tick count (0 immediately after NewInstance).
func (i *Instance) Tick() uint64 {
	return i.tickCount.Load()
}

// Stop halts the tick loop. Safe to call multiple times. Does not affect
// connected players or pending broadcasts — those continue running on
// their own goroutines.
func (i *Instance) Stop() {
	i.stopOnce.Do(func() {
		close(i.stopTick)
	})
}

func (i *Instance) tickLoop() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-i.stopTick:
			return
		case <-ticker.C:
			i.runTick()
		}
	}
}

func (i *Instance) runTick() {
	tick := i.tickCount.Add(1)

	// Snapshot the handler list so a handler can OnTick a new subscriber
	// without deadlocking, and so we don't hold the lock during user code.
	i.tickMu.RLock()
	handlers := make([]TickHandler, len(i.tickHandlers))
	copy(handlers, i.tickHandlers)
	i.tickMu.RUnlock()

	for _, h := range handlers {
		// Isolate panics: one buggy game shouldn't freeze the instance.
		safeTick(i, h, tick)
	}
}

func safeTick(i *Instance, h TickHandler, tick uint64) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("instance %s tick handler panic at tick %d: %v\n",
				i.ID, tick, r)
		}
	}()
	h(tick)
}

// JoinAndAnnounce registers c in this instance's player list and sends the
// visibility packets that make c and the existing players mutually
// visible. Run AFTER c.player and c.instance are set. Fires OnPlayerJoin
// once everything is wired up so game logic sees a fully-joined player.
func (i *Instance) JoinAndAnnounce(c *ClientConnection) {
	i.joinMu.Lock()

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

	hook := i.OnPlayerJoin
	i.joinMu.Unlock()

	// Hook fires outside the lock so game logic can call back into the
	// instance without deadlocking.
	if hook != nil {
		safeHook(i, "OnPlayerJoin", func() { hook(c) })
	}
}

// LeaveAndAnnounce removes the player from the instance and tells the
// remaining players to despawn them. Fires OnPlayerLeave BEFORE the player
// is dropped so game logic can still see them in the list. No-op when
// c.player is nil (early disconnect before login completed).
func (i *Instance) LeaveAndAnnounce(c *ClientConnection) {
	if c.player == nil {
		return
	}

	// Hook outside joinMu so game cleanup can call into the instance.
	if hook := i.OnPlayerLeave; hook != nil {
		safeHook(i, "OnPlayerLeave", func() { hook(c) })
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

// safeHook runs a hook function with panic recovery and a labelled log
// line, so a buggy game can't kill the calling goroutine.
func safeHook(i *Instance, name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("instance %s %s hook panic: %v\n", i.ID, name, r)
		}
	}()
	fn()
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
