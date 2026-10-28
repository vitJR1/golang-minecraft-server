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

// leaveFrom puts name into a fresh instance running gameID (via
// /instance join, on the player's own read loop), sends /leave and returns
// the server once the player is somewhere else.
func leaveFrom(t *testing.T, name, gameID string) (*Server, *Instance) {
	t.Helper()
	s := New()
	SetupLobbies(s)
	s.Ops.Add(name)
	game := NewInstance("round-1", s, world.NewMemoryWorld())
	game.GameID = gameID
	s.AddInstance(game)
	t.Cleanup(game.Stop)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, name)
	cli.startDiscardDrain()
	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance join round-1"))
	waitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer(name)
		return ok && inst == game
	}, name+" to enter the round")

	cli.write(t, SbPlayChatCommand, protocol.WriteString("leave"))
	var landed *Instance
	waitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer(name)
		landed = inst
		return ok && inst != game
	}, name+" to leave the round")
	return s, landed
}

func TestLeaveBedWarsGoesToBedWarsLobby(t *testing.T) {
	for _, kind := range []string{"bedwars", "bedwars-1x1"} {
		s, landed := leaveFrom(t, "Quitter", kind)
		if landed != s.GetInstance(LobbyBedWars) {
			t.Errorf("/leave from %s landed in %s, want the BedWars lobby", kind, landed.ID)
		}
	}
}

func TestLeaveFFAGoesToFFALobby(t *testing.T) {
	s, landed := leaveFrom(t, "Duelist", "ffa")
	if landed != s.GetInstance(LobbyFFA) {
		t.Errorf("/leave from ffa landed in %s, want the FFA lobby", landed.ID)
	}
}

func TestLeaveUnknownGameGoesToHub(t *testing.T) {
	s, landed := leaveFrom(t, "Lost", "")
	if landed != s.Hub {
		t.Errorf("/leave from a plain instance landed in %s, want the hub", landed.ID)
	}
}

func TestLeaveInHubIsNoop(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Homebody")
	ch := cli.startDrain()
	drainExpect(t, ch, "Homebody bootstrap", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("leave"))
	drainExpect(t, ch, "not-in-a-game reply", CbPlaySystemChat)
	if _, inst, _ := s.FindPlayer("Homebody"); inst != s.Hub {
		t.Error("Homebody should still be in the hub")
	}
}

func TestLobbyForGame(t *testing.T) {
	cases := map[string]string{
		"bedwars": LobbyBedWars, "bedwars-1x1": LobbyBedWars, "bedwars-2x5": LobbyBedWars,
		"ffa": LobbyFFA, "skywars-solo": LobbySkyWars,
	}
	for game, want := range cases {
		if got, ok := lobbyForGame(game); !ok || got != want {
			t.Errorf("lobbyForGame(%q) = %q %v, want %q", game, got, ok, want)
		}
	}
	for _, game := range []string{"", "bedwarsish", "parkour"} {
		if got, ok := lobbyForGame(game); ok {
			t.Errorf("lobbyForGame(%q) = %q, want none", game, got)
		}
	}
}
