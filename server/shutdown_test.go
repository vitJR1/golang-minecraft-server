package server

import (
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/instances/tickloop"
	"testing"
	"time"
)

func TestShutdownKicksPlayersAndStopsTicks(t *testing.T) {
	s := New()
	cliA := pipeClientOn(t, s)
	completeOfflineLogin(t, cliA, "Alice")
	idsA := cliA.StartDrain()
	cliB := pipeClientOn(t, s)
	completeOfflineLogin(t, cliB, "Bob")
	cliB.StartDiscardDrain()

	s.Shutdown("Server is restarting")

	waitFor(t, 2*time.Second, func() bool { return s.PlayerCount() == 0 },
		"all players to be kicked")

	// The drain channel closes when the server closes Alice's pipe; the
	// Play Disconnect must have gone on the wire before that.
	sawDisconnect := false
	for id := range idsA {
		if id == packetid.CbPlayDisconnect {
			sawDisconnect = true
		}
	}
	if !sawDisconnect {
		t.Error("expected a Play Disconnect packet before the connection closed")
	}

	// Tick loops are stopped: the hub's counter freezes.
	tick := s.Hub.Tick()
	time.Sleep(3 * tickloop.Interval)
	if got := s.Hub.Tick(); got != tick {
		t.Errorf("hub tick advanced after shutdown: %d -> %d", tick, got)
	}
}
