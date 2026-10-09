package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestStatsEndpoint(t *testing.T) {
	s := New()
	cliA := pipeClientOn(t, s)
	completeOfflineLogin(t, cliA, "Watcher")
	cliA.startDiscardDrain()

	lis, err := StartMetrics(s, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start metrics: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	url := fmt.Sprintf("http://%s/stats", lis.Addr())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	var stats serverStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
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
	resp2, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /stats #2: %v", err)
	}
	defer resp2.Body.Close()
	if err := json.NewDecoder(resp2.Body).Decode(&stats); err != nil {
		t.Fatalf("decode #2: %v", err)
	}
	if stats.WorstTickMs < 0 {
		t.Error("worst tick must be non-negative")
	}
}

func TestPprofEndpoint(t *testing.T) {
	s := New()
	lis, err := StartMetrics(s, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start metrics: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	resp, err := http.Get(fmt.Sprintf("http://%s/debug/pprof/", lis.Addr()))
	if err != nil {
		t.Fatalf("GET pprof index: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pprof index status: %d", resp.StatusCode)
	}
}
