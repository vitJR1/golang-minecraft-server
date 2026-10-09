package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"
)

// serverStats is the /stats JSON shape — a point-in-time operational
// snapshot. Field set is intentionally small: what an operator needs to
// judge "is the server healthy" at a glance and what a dashboard can
// scrape cheaply.
type serverStats struct {
	UptimeSeconds int64 `json:"uptime_seconds"`

	Players   int            `json:"players"`
	Instances map[string]int `json:"instances"` // instance ID → player count

	// ConnsRejected counts sockets bounced by the flood limiter;
	// QueueFullKicks counts players dropped because their outbound queue
	// overflowed (slow client, or the server writing faster than the link).
	// Both monotonically increase since process start.
	ConnsRejected  uint64 `json:"conns_rejected"`
	QueueFullKicks uint64 `json:"queue_full_kicks"`

	// WorstTickMs is the slowest single tick across all live instances
	// since each started; LastTickMs is the worst *most-recent* tick.
	// Sustained values near 50 (the tick interval) mean game logic is
	// eating the whole tick budget.
	WorstTickMs float64 `json:"worst_tick_ms"`
	LastTickMs  float64 `json:"last_tick_ms"`

	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
}

func (s *Server) statsSnapshot() serverStats {
	s.mu.RLock()
	instances := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		instances = append(instances, i)
	}
	s.mu.RUnlock()

	perInstance := make(map[string]int, len(instances))
	players := 0
	var worstTick, lastTick int64
	for _, i := range instances {
		n := i.Players.Count()
		perInstance[i.ID] = n
		players += n
		worstTick = max(worstTick, i.maxTickNanos.Load())
		lastTick = max(lastTick, i.lastTickNanos.Load())
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return serverStats{
		UptimeSeconds:  int64(time.Since(s.startTime).Seconds()),
		Players:        players,
		Instances:      perInstance,
		ConnsRejected:  s.connsRejected.Load(),
		QueueFullKicks: s.queueFullKicks.Load(),
		WorstTickMs:    float64(worstTick) / 1e6,
		LastTickMs:     float64(lastTick) / 1e6,
		Goroutines:     runtime.NumGoroutine(),
		HeapAllocMB:    float64(mem.HeapAlloc) / (1 << 20),
	}
}

// StartMetrics serves the ops endpoints on addr and returns the bound
// listener (so callers can learn the port when addr uses :0):
//
//	/stats          — JSON snapshot of live server state (serverStats)
//	/debug/pprof/*  — Go profiling (CPU, heap, goroutines, …)
//
// There is NO authentication — bind to loopback (the default) and reach it
// over SSH; never expose this port to the internet.
func StartMetrics(s *Server, addr string) (net.Listener, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.statsSnapshot())
	})

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	go func() {
		// net.ErrClosed is the normal shutdown path (listener closed).
		if err := http.Serve(lis, mux); err != nil && !errors.Is(err, net.ErrClosed) {
			slog.Error("metrics server failed", "err", err)
		}
	}()
	return lis, nil
}
