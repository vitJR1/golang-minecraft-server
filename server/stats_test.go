package server

import (
	"testing"
	"time"
)

func TestStats(t *testing.T) {
	s := New()
	cliA := pipeClientOn(t, s)
	completeOfflineLogin(t, cliA, "Watcher")
	cliA.StartDiscardDrain()

	stats := s.Stats()
	if stats.Players != 1 {
		t.Errorf("players: got %d, want 1", stats.Players)
	}
	if stats.Instances["hub"] != 1 {
		t.Errorf("hub players: got %d, want 1", stats.Instances["hub"])
	}
	if stats.Goroutines <= 0 {
		t.Error("goroutines should be positive")
	}

	// Tick timings populate once the hub has ticked at least once.
	waitFor(t, time.Second, func() bool { return s.Hub.Tick() > 0 }, "hub to tick")
	stats = s.Stats()
	if stats.WorstTickMs < 0 {
		t.Error("worst tick must be non-negative")
	}
}
