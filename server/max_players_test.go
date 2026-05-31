package server

import (
	"minecraft-server/cfg"
	"minecraft-server/protocol"
	"testing"
	"time"
)

// withMaxPlayers temporarily reduces cfg.MaxPlayers for a single test.
// Restored on cleanup so other tests aren't affected.
func withMaxPlayers(t *testing.T, n int) {
	t.Helper()
	orig := cfg.MaxPlayers
	cfg.MaxPlayers = n
	t.Cleanup(func() { cfg.MaxPlayers = orig })
}

// TestMaxPlayersAllowsUpToCap: with MaxPlayers=2, two non-op players
// connect successfully and both land in PlayerList.
func TestMaxPlayersAllowsUpToCap(t *testing.T) {
	withMaxPlayers(t, 2)
	s := New()

	a := pipeClientOn(t, s)
	completeOfflineLogin(t, a, "Alice")
	a.startDiscardDrain()

	b := pipeClientOn(t, s)
	completeOfflineLogin(t, b, "Bob")
	b.startDiscardDrain()

	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 2 },
		"both Alice and Bob to be online")
}

// TestMaxPlayersRejectsOverCap: third player gets a Login Disconnect and
// never enters PlayerList.
func TestMaxPlayersRejectsOverCap(t *testing.T) {
	withMaxPlayers(t, 2)
	s := New()

	a := pipeClientOn(t, s)
	completeOfflineLogin(t, a, "Alice")
	a.startDiscardDrain()
	b := pipeClientOn(t, s)
	completeOfflineLogin(t, b, "Bob")
	b.startDiscardDrain()

	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 2 }, "2/2 online")

	// Third tries to join — must be kicked with Login Disconnect.
	c := pipeClientOn(t, s)
	c.write(t, SbHandshake, buildHandshake(763, "localhost", 25565, 2))
	c.write(t, SbLoginStart, protocol.WriteString("Charlie"))

	// First reply is the disconnect packet.
	id, _ := c.read(t)
	if id != CbLoginDisconnect {
		t.Fatalf("expected LoginDisconnect (0x%02X), got 0x%02X", CbLoginDisconnect, id)
	}

	// Server-side: Charlie never registered.
	time.Sleep(100 * time.Millisecond)
	if _, _, ok := s.FindPlayer("Charlie"); ok {
		t.Error("Charlie should not have been added to PlayerList")
	}
	if s.PlayerCount() != 2 {
		t.Errorf("PlayerCount: got %d, want 2", s.PlayerCount())
	}
}

// TestMaxPlayersOpsBypassCap: an op name gets through even when the
// server is full — admins must always be able to rescue.
func TestMaxPlayersOpsBypassCap(t *testing.T) {
	withMaxPlayers(t, 1)
	s := New()
	s.Ops.Add("AdminBob")

	a := pipeClientOn(t, s)
	completeOfflineLogin(t, a, "Alice")
	a.startDiscardDrain()
	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 1 }, "1/1")

	// AdminBob is op → bypass.
	b := pipeClientOn(t, s)
	completeOfflineLogin(t, b, "AdminBob")
	b.startDiscardDrain()

	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 2 },
		"AdminBob to bypass cap")
}

// TestMaxPlayersZeroMeansUnlimited: cap of 0 disables the gate.
func TestMaxPlayersZeroMeansUnlimited(t *testing.T) {
	withMaxPlayers(t, 0)
	s := New()

	for _, name := range []string{"A", "B", "C", "D"} {
		cli := pipeClientOn(t, s)
		completeOfflineLogin(t, cli, name)
		cli.startDiscardDrain()
	}
	waitFor(t, 2*time.Second, func() bool { return s.PlayerCount() == 4 },
		"all 4 players to be online")
}
