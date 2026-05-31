package server

import (
	"fmt"
	"log/slog"
	"minecraft-server/game"
	"sync"
)

// Matchmaker holds the per-game waiting queues. When a queue reaches a
// game's MinPlayers it spawns a fresh Instance via Server.StartGame and
// moves the waiting players in.
//
// Concurrency: the public methods (Queue, Dequeue, …) are safe to call from
// any goroutine. Internally a single mutex guards both `queues` and
// `member`; the work that crosses goroutines (StartGame, MovePlayer) runs
// outside the lock to avoid blocking other queue mutations.
type Matchmaker struct {
	server *Server

	mu     sync.Mutex
	queues map[string][]*ClientConnection // gameID → waiting players (FIFO)
	member map[*ClientConnection]string   // c → gameID it's currently queued for
}

// NewMatchmaker constructs a Matchmaker bound to a Server. Called by
// Server.New; not intended for direct use.
func NewMatchmaker(s *Server) *Matchmaker {
	return &Matchmaker{
		server: s,
		queues: make(map[string][]*ClientConnection),
		member: make(map[*ClientConnection]string),
	}
}

// Queue adds c to gameID's waiting list. Errors when the game isn't
// registered, when c is already queued elsewhere, or when the queue is
// full (>= MaxPlayers for that game).
//
// If the queue now has at least MinPlayers, takes up to MaxPlayers from
// the front of the queue, starts a new Instance, and asynchronously moves
// each picked player into it. The move runs in its own goroutine because
// MovePlayer must be invoked from the player's own readLoop and the
// matchmaker doesn't know which goroutine its caller is on.
func (m *Matchmaker) Queue(c *ClientConnection, gameID string) error {
	def, ok := game.GetDef(gameID)
	if !ok {
		return fmt.Errorf("no such game: %s", gameID)
	}

	m.mu.Lock()
	if existing, alreadyQueued := m.member[c]; alreadyQueued {
		m.mu.Unlock()
		return fmt.Errorf("already queued for %s", existing)
	}
	q := m.queues[gameID]
	if def.MaxPlayers > 0 && len(q) >= def.MaxPlayers {
		m.mu.Unlock()
		return fmt.Errorf("queue for %s is full", gameID)
	}
	q = append(q, c)
	m.queues[gameID] = q
	m.member[c] = gameID

	var toStart []*ClientConnection
	if def.MinPlayers > 0 && len(q) >= def.MinPlayers {
		n := len(q)
		if def.MaxPlayers > 0 && n > def.MaxPlayers {
			n = def.MaxPlayers
		}
		toStart = make([]*ClientConnection, n)
		copy(toStart, q[:n])
		m.queues[gameID] = q[n:]
		for _, picked := range toStart {
			delete(m.member, picked)
		}
	}
	m.mu.Unlock()

	if len(toStart) == 0 {
		return nil
	}
	go m.startGame(def, toStart)
	return nil
}

// startGame creates a fresh Instance from the definition's template,
// attaches the game's Logic, then ferries each player in. Runs in its own
// goroutine because both Server.StartGame and MovePlayer can be slow.
func (m *Matchmaker) startGame(def *game.Definition, players []*ClientConnection) {
	inst, err := m.server.StartGame(def.ID)
	if err != nil {
		slog.Error("matchmaker: StartGame failed",
			"game", def.ID, "players", len(players), "err", err)
		for _, c := range players {
			_ = c.sendSystemMessage("Failed to start " + def.Name + ": " + err.Error())
		}
		return
	}
	slog.Info("matchmaker: starting game",
		"game", def.ID, "instance", inst.ID, "players", len(players))

	for _, c := range players {
		cc := c
		go func() {
			if err := m.server.MovePlayer(cc, inst, 0, 80, 0); err != nil {
				slog.Warn("matchmaker: move failed",
					"player", cc.playerName, "instance", inst.ID, "err", err)
				_ = cc.sendSystemMessage("Couldn't enter " + def.Name + ": " + err.Error())
			}
		}()
	}
}

// Dequeue removes c from whatever queue it's in, if any. Safe to call
// when c is not queued. Called by cleanup on disconnect.
func (m *Matchmaker) Dequeue(c *ClientConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	gameID, ok := m.member[c]
	if !ok {
		return
	}
	delete(m.member, c)
	q := m.queues[gameID]
	for i, qc := range q {
		if qc == c {
			m.queues[gameID] = append(q[:i], q[i+1:]...)
			break
		}
	}
}

// QueueSize returns how many players are currently waiting for gameID.
func (m *Matchmaker) QueueSize(gameID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queues[gameID])
}

// PlayerQueue returns the game ID c is queued for, or ("", false) if c
// isn't waiting.
func (m *Matchmaker) PlayerQueue(c *ClientConnection) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	gameID, ok := m.member[c]
	return gameID, ok
}
