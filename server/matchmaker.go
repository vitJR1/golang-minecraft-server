package server

import (
	"fmt"
	"log/slog"
	"minecraft-server/game"
	"minecraft-server/server/instances/matchmaker"
)

// Matchmaker holds the per-game waiting queues (server/instances/matchmaker). When a
// queue reaches a game's MinPlayers it spawns a fresh Instance via
// Server.StartGame and moves the waiting players in.
//
// Concurrency: the public methods (Queue, Dequeue, …) are safe to call from
// any goroutine. The queues have their own lock; the work that crosses
// goroutines (StartGame, MovePlayer) runs outside it.
type Matchmaker struct {
	server *Server
	queues matchmaker.Queues[*ClientConnection]
}

// NewMatchmaker constructs a Matchmaker bound to a Server. Called by
// Server.New; not intended for direct use.
func NewMatchmaker(s *Server) *Matchmaker {
	return &Matchmaker{server: s}
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
	toStart, err := m.queues.Join(c, gameID, def.MinPlayers, def.MaxPlayers)
	if err != nil {
		return err
	}
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

	// Move players in one at a time, NOT concurrently: each MovePlayer sends a
	// Respawn (which wipes the client's entities) before announcing the joiner.
	// Overlapping moves let one player's Respawn arrive after another player's
	// Spawn was already broadcast to them, so they'd never see each other.
	// Sequential moves keep the per-client order correct.
	for _, c := range players {
		if err := m.server.Join(c, inst); err != nil {
			slog.Warn("matchmaker: move failed",
				"player", c.playerName, "instance", inst.ID, "err", err)
			_ = c.sendSystemMessage("Couldn't enter " + def.Name + ": " + err.Error())
		}
	}
}

// Dequeue removes c from whatever queue it's in, if any. Safe to call
// when c is not queued. Called by cleanup on disconnect.
func (m *Matchmaker) Dequeue(c *ClientConnection) { m.queues.Leave(c) }

// QueueSize returns how many players are currently waiting for gameID.
func (m *Matchmaker) QueueSize(gameID string) int { return m.queues.Size(gameID) }

// PlayerQueue returns the game ID c is queued for, or ("", false) if c
// isn't waiting.
func (m *Matchmaker) PlayerQueue(c *ClientConnection) (string, bool) { return m.queues.Of(c) }
