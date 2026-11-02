package metrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

type fixed Stats

func (f fixed) Stats() Stats { return Stats(f) }

func TestStatsEndpoint(t *testing.T) {
	lis, err := Start(fixed{Players: 3, Instances: map[string]int{"hub": 3}, Goroutines: 7}, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start metrics: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	resp, err := http.Get(fmt.Sprintf("http://%s/stats", lis.Addr()))
	if err != nil {
		t.Fatalf("GET /stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var got Stats
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Players != 3 || got.Instances["hub"] != 3 || got.Goroutines != 7 {
		t.Errorf("stats %+v", got)
	}
}

func TestPprofEndpoint(t *testing.T) {
	lis, err := Start(fixed{}, "127.0.0.1:0")
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
