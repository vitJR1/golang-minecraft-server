package server

import (
	"minecraft-server/game"
	"minecraft-server/world"
	"testing"
	"time"
)

// registerMMGame registers a tiny game definition for the duration of one
// test. ID is unique-per-test so we never collide with the global registry
// (which has no public reset).
func registerMMGame(t *testing.T, id string, min, max int) {
	t.Helper()
	game.Register(&game.Definition{
		ID:         id,
		Name:       "MM Test " + id,
		MinPlayers: min,
		MaxPlayers: max,
		Template:   world.NewTemplate(),
		New:        func() game.Logic { return game.NoopLogic{} },
	})
}

func TestMatchmakerQueueHoldsBelowMin(t *testing.T) {
	registerMMGame(t, "mm-hold", 2, 4)
	s := New()

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Solo")
	cli.startDiscardDrain()

	if err := s.Matchmaker.Queue(findConn(t, s, "Solo"), "mm-hold"); err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if got := s.Matchmaker.QueueSize("mm-hold"); got != 1 {
		t.Errorf("queue size: got %d, want 1", got)
	}
	if gameID, ok := s.Matchmaker.PlayerQueue(findConn(t, s, "Solo")); !ok || gameID != "mm-hold" {
		t.Errorf("PlayerQueue: got (%q, %v), want (mm-hold, true)", gameID, ok)
	}
}

func TestMatchmakerStartsGameAtMin(t *testing.T) {
	registerMMGame(t, "mm-start", 2, 4)
	s := New()

	a := pipeClientOn(t, s)
	completeOfflineLogin(t, a, "Alice")
	a.startDiscardDrain()
	b := pipeClientOn(t, s)
	completeOfflineLogin(t, b, "Bob")
	b.startDiscardDrain()

	connA := findConn(t, s, "Alice")
	connB := findConn(t, s, "Bob")

	// First queue: held.
	if err := s.Matchmaker.Queue(connA, "mm-start"); err != nil {
		t.Fatalf("Queue Alice: %v", err)
	}
	if s.Matchmaker.QueueSize("mm-start") != 1 {
		t.Fatalf("queue size after 1: got %d, want 1", s.Matchmaker.QueueSize("mm-start"))
	}

	// Second queue: trips the threshold; both should be picked.
	if err := s.Matchmaker.Queue(connB, "mm-start"); err != nil {
		t.Fatalf("Queue Bob: %v", err)
	}

	// Game start is asynchronous: StartGame creates the instance in one
	// goroutine, then each player is MovePlayer'd from yet another. Wait
	// for the instance to appear AND fill up before asserting.
	var instID string
	waitFor(t, 2*time.Second, func() bool {
		for _, id := range s.InstanceIDs() {
			if id != "hub" {
				instID = id
				return true
			}
		}
		return false
	}, "game instance created")

	waitFor(t, 2*time.Second, func() bool {
		inst := s.GetInstance(instID)
		return inst != nil && inst.Players.Count() == 2
	}, "both players to land in game instance")

	if s.Hub.Players.Count() != 0 {
		t.Errorf("hub should be empty after both moved: count=%d", s.Hub.Players.Count())
	}
	if s.Matchmaker.QueueSize("mm-start") != 0 {
		t.Errorf("queue should be empty after start: got %d", s.Matchmaker.QueueSize("mm-start"))
	}
}

func TestMatchmakerDequeue(t *testing.T) {
	registerMMGame(t, "mm-dequeue", 4, 8)
	s := New()

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Quitter")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Quitter")

	_ = s.Matchmaker.Queue(conn, "mm-dequeue")
	if s.Matchmaker.QueueSize("mm-dequeue") != 1 {
		t.Fatalf("pre-dequeue size: got %d, want 1", s.Matchmaker.QueueSize("mm-dequeue"))
	}
	s.Matchmaker.Dequeue(conn)
	if s.Matchmaker.QueueSize("mm-dequeue") != 0 {
		t.Errorf("post-dequeue size: got %d, want 0", s.Matchmaker.QueueSize("mm-dequeue"))
	}
	if _, ok := s.Matchmaker.PlayerQueue(conn); ok {
		t.Error("PlayerQueue should return false after dequeue")
	}
}

func TestMatchmakerDoubleQueueRejected(t *testing.T) {
	registerMMGame(t, "mm-double-a", 4, 8)
	registerMMGame(t, "mm-double-b", 4, 8)
	s := New()

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Greedy")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Greedy")

	if err := s.Matchmaker.Queue(conn, "mm-double-a"); err != nil {
		t.Fatalf("first queue: %v", err)
	}
	if err := s.Matchmaker.Queue(conn, "mm-double-b"); err == nil {
		t.Error("expected error queueing same conn into a second game")
	}
}

func TestMatchmakerUnknownGameRejected(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Lost")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Lost")

	if err := s.Matchmaker.Queue(conn, "mm-nope-doesnt-exist"); err == nil {
		t.Error("expected error queueing for unregistered game")
	}
}

func TestMatchmakerDisconnectDequeues(t *testing.T) {
	registerMMGame(t, "mm-disconnect", 4, 8)
	s := New()

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Ghost")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Ghost")

	_ = s.Matchmaker.Queue(conn, "mm-disconnect")
	if s.Matchmaker.QueueSize("mm-disconnect") != 1 {
		t.Fatalf("pre-disconnect: got %d, want 1", s.Matchmaker.QueueSize("mm-disconnect"))
	}

	// Close from the client side — server's cleanup runs and should
	// drop us from the queue.
	_ = cli.conn.Close()
	waitFor(t, time.Second, func() bool {
		return s.Matchmaker.QueueSize("mm-disconnect") == 0
	}, "cleanup to dequeue")
}

// findConn looks the connection up by player name. Tests routinely need to
// reach for the server-side *ClientConnection of a freshly-logged-in
// player so they can poke the matchmaker directly.
func findConn(t *testing.T, s *Server, name string) *ClientConnection {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c, _, ok := s.FindPlayer(name); ok {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("connection for %q never registered", name)
	return nil
}
