package server

import (
	"minecraft-server/protocol"
	"minecraft-server/world"
	"testing"
	"time"
)

// TestCmdHubFromArenaReturnsToHub: player in a non-hub instance issues
// /hub and ends up back in the hub. The arena trip goes through
// /instance join (not direct MovePlayer) so the move runs on the
// player's own readLoop and doesn't race c.instance.
func TestCmdHubFromArenaReturnsToHub(t *testing.T) {
	s := New()
	s.Ops.Add("Wanderer")
	arena := NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)
	t.Cleanup(arena.Stop)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Wanderer")
	cli.startDiscardDrain()

	// Step 1: arena via wire.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance join arena"))
	waitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("Wanderer")
		return ok && inst == arena
	}, "Wanderer to enter arena")

	// Step 2: /hub back.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("hub"))
	waitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("Wanderer")
		return ok && inst == s.Hub
	}, "Wanderer to return to hub")
}

// TestCmdHubWhileInHubIsNoop: /hub from hub keeps you put, just sends a
// SystemChat reply.
func TestCmdHubWhileInHubIsNoop(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Idler")
	ch := cli.startDrain()
	drainExpect(t, ch, "Idler bootstrap", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("hub"))
	drainExpect(t, ch, "already-in-hub reply", CbPlaySystemChat)

	if _, inst, _ := s.FindPlayer("Idler"); inst != s.Hub {
		t.Errorf("Idler should still be in hub")
	}
}

// TestCmdHubClearsMatchmakerQueue: queueing for a game then calling /hub
// drops the queue entry.
func TestCmdHubClearsMatchmakerQueue(t *testing.T) {
	registerMMGame(t, "mm-hub-cancel", 4, 8)

	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Quitter")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Quitter")

	_ = s.Matchmaker.Queue(conn, "mm-hub-cancel")
	if s.Matchmaker.QueueSize("mm-hub-cancel") != 1 {
		t.Fatalf("precondition: queue size %d", s.Matchmaker.QueueSize("mm-hub-cancel"))
	}

	cli.write(t, SbPlayChatCommand, protocol.WriteString("hub"))

	waitFor(t, time.Second, func() bool {
		return s.Matchmaker.QueueSize("mm-hub-cancel") == 0
	}, "matchmaker queue to clear")
}
