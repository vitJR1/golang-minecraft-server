package commands

import (
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server"
	"minecraft-server/server/lobby"
	"minecraft-server/server/servertest"
	"minecraft-server/world"
	"testing"
	"time"
)

// TestCmdHubFromArenaReturnsToHub: player in a non-hub instance issues
// /hub and ends up back in the hub. The arena trip goes through
// /instance join (not direct MovePlayer) so the move runs on the
// player's own readLoop and doesn't race c.Instance().
func TestCmdHubFromArenaReturnsToHub(t *testing.T) {
	s := server.New()
	s.Ops.Add("Wanderer")
	arena := server.NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)
	t.Cleanup(arena.Stop)

	cli := servertest.Dial(t, s)
	cli.Login(t, "Wanderer")
	cli.StartDiscardDrain()

	// Step 1: arena via wire.
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance join arena"))
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("Wanderer")
		return ok && inst == arena
	}, "Wanderer to enter arena")

	// Step 2: /hub back.
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("hub"))
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("Wanderer")
		return ok && inst == s.Hub
	}, "Wanderer to return to hub")
}

// TestCmdHubClearsMatchmakerQueue: queueing for a game then calling /hub
// drops the queue entry.
func TestCmdHubClearsMatchmakerQueue(t *testing.T) {
	registerMMGame(t, "mm-hub-cancel", 4, 8)

	s := server.New()
	cli := servertest.Dial(t, s)
	cli.Login(t, "Quitter")
	cli.StartDiscardDrain()
	conn := findConn(t, s, "Quitter")

	_ = s.Matchmaker.Queue(conn, "mm-hub-cancel")
	if s.Matchmaker.QueueSize("mm-hub-cancel") != 1 {
		t.Fatalf("precondition: queue size %d", s.Matchmaker.QueueSize("mm-hub-cancel"))
	}

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("hub"))

	servertest.WaitFor(t, time.Second, func() bool {
		return s.Matchmaker.QueueSize("mm-hub-cancel") == 0
	}, "matchmaker queue to clear")
}

// leaveFrom puts name into a fresh instance running gameID (via
// /instance join, on the player's own read loop), sends /leave and returns
// the server once the player is somewhere else.
func leaveFrom(t *testing.T, name, gameID string) (*server.Server, *server.Instance) {
	t.Helper()
	s := server.New()
	server.SetupLobbies(s)
	s.Ops.Add(name)
	game := server.NewInstance("round-1", s, world.NewMemoryWorld())
	game.GameID = gameID
	s.AddInstance(game)
	t.Cleanup(game.Stop)

	cli := servertest.Dial(t, s)
	cli.Login(t, name)
	cli.StartDiscardDrain()
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance join round-1"))
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer(name)
		return ok && inst == game
	}, name+" to enter the round")

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("leave"))
	var landed *server.Instance
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer(name)
		landed = inst
		return ok && inst != game
	}, name+" to leave the round")
	return s, landed
}

func TestLeaveBedWarsGoesToBedWarsLobby(t *testing.T) {
	for _, kind := range []string{"bedwars", "bedwars-1x1"} {
		s, landed := leaveFrom(t, "Quitter", kind)
		if landed != s.GetInstance(lobby.BedWars) {
			t.Errorf("/leave from %s landed in %s, want the BedWars lobby", kind, landed.ID)
		}
	}
}

func TestLeaveFFAGoesToFFALobby(t *testing.T) {
	s, landed := leaveFrom(t, "Duelist", "ffa")
	if landed != s.GetInstance(lobby.FFA) {
		t.Errorf("/leave from ffa landed in %s, want the FFA lobby", landed.ID)
	}
}

func TestLeaveUnknownGameGoesToHub(t *testing.T) {
	s, landed := leaveFrom(t, "Lost", "")
	if landed != s.Hub {
		t.Errorf("/leave from a plain instance landed in %s, want the hub", landed.ID)
	}
}

func TestForceStartCommand(t *testing.T) {
	s := server.New()
	s.Ops.Add("Admin")
	_, admin := login(t, s, "Admin")
	_, plain := login(t, s, "Player")

	s.RunCommand(admin, "forcestart") // the hub has no round to start
	calls := 0
	s.Hub.OnForceStart = func() error { calls++; return nil }
	s.RunCommand(plain, "forcestart") // not an op
	if calls != 0 {
		t.Fatal("a non-op force-started the game")
	}
	s.RunCommand(admin, "forcestart")
	if calls != 1 {
		t.Fatalf("OnForceStart called %d times, want 1", calls)
	}
}

// TestCmdHubWhileInHubIsNoop: /hub from hub keeps you put, just sends a
// SystemChat reply.
func TestCmdHubWhileInHubIsNoop(t *testing.T) {
	s := server.New()
	cli := servertest.Dial(t, s)
	cli.Login(t, "Idler")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Idler bootstrap", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("hub"))
	servertest.DrainExpect(t, ch, "already-in-hub reply", packetid.CbPlaySystemChat)

	if _, inst, _ := s.FindPlayer("Idler"); inst != s.Hub {
		t.Errorf("Idler should still be in hub")
	}
}

func TestLeaveInHubIsNoop(t *testing.T) {
	s := server.New()
	cli := servertest.Dial(t, s)
	cli.Login(t, "Homebody")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Homebody bootstrap", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("leave"))
	servertest.DrainExpect(t, ch, "not-in-a-game reply", packetid.CbPlaySystemChat)
	if _, inst, _ := s.FindPlayer("Homebody"); inst != s.Hub {
		t.Error("Homebody should still be in the hub")
	}
}
