package server

import (
	"fmt"
	"sync"
)

// PlayerList tracks the connections of every logged-in player. Concurrent-
// safe via an RWMutex. Stores *ClientConnection (rather than *player.Player)
// because almost every reason to look a player up — broadcasting a packet,
// kicking them, checking their wire state — needs the connection too.
type PlayerList struct {
	mu sync.RWMutex
	// by holds connections keyed by entity ID. Entity IDs are unique per
	// process (server.nextEntityID), so this is collision-free.
	by map[int32]*ClientConnection
}

func NewPlayerList() *PlayerList {
	return &PlayerList{by: make(map[int32]*ClientConnection)}
}

// Add registers a connection. Caller must have c.player set already.
// Subsequent Add of the same entity ID overwrites the previous entry.
func (pl *PlayerList) Add(c *ClientConnection) {
	pl.mu.Lock()
	pl.by[c.player.EntityID] = c
	pl.mu.Unlock()
}

// Remove drops a player from the list. No-op if absent.
func (pl *PlayerList) Remove(entityID int32) {
	pl.mu.Lock()
	delete(pl.by, entityID)
	pl.mu.Unlock()
}

func (pl *PlayerList) Get(entityID int32) (*ClientConnection, bool) {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	c, ok := pl.by[entityID]
	return c, ok
}

// ByName is a linear scan — fine for small player counts. Switch to a second
// index if we ever care about lookup performance at >100 players.
func (pl *PlayerList) ByName(name string) (*ClientConnection, bool) {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	for _, c := range pl.by {
		if c.player.Name == name {
			return c, true
		}
	}
	return nil, false
}

func (pl *PlayerList) Count() int {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return len(pl.by)
}

// UUIDs returns a snapshot of every player's UUID currently in the list.
// Used by Server.MovePlayer to tell a departing client which tab-list
// entries to clear before the new instance announces its set.
func (pl *PlayerList) UUIDs() [][16]byte {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	out := make([][16]byte, 0, len(pl.by))
	for _, c := range pl.by {
		out = append(out, c.player.UUID)
	}
	return out
}

// snapshot returns a copy of the current connection set so callers can
// iterate without holding the mutex.
func (pl *PlayerList) snapshot() []*ClientConnection {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	out := make([]*ClientConnection, 0, len(pl.by))
	for _, c := range pl.by {
		out = append(out, c)
	}
	return out
}

// Range visits every connection. The callback runs without the list lock
// held, so callbacks may safely take longer-running actions (network writes,
// further lookups). Order is non-deterministic.
func (pl *PlayerList) Range(fn func(c *ClientConnection)) {
	for _, c := range pl.snapshot() {
		fn(c)
	}
}

// Broadcast sends a packet to every player. If exceptEntityID >= 0, the
// connection with that entity ID is skipped — typical for "tell everyone
// else what this player just did".
//
// Errors from individual writes are logged but not surfaced; one slow or
// dead client should not block the rest. Each safeWrite has its own
// 5-second write deadline.
func (pl *PlayerList) Broadcast(packetID int32, payload []byte, exceptEntityID int32) {
	for _, c := range pl.snapshot() {
		if c.player.EntityID == exceptEntityID {
			continue
		}
		if err := c.safeWrite(packetID, payload); err != nil {
			fmt.Printf("broadcast to %s: %v\n", c.player.Name, err)
		}
	}
}
