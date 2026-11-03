// Package metrics serves the ops endpoints: /stats (a JSON snapshot of the
// server's state, taken by whatever implements Source — server.Server) and
// Go's pprof.
package metrics

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"
)

// Stats is the /stats JSON shape — a point-in-time operational
// snapshot. Field set is intentionally small: what an operator needs to
// judge "is the server healthy" at a glance and what a dashboard can
// scrape cheaply.
type Stats struct {
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

// Source takes a Stats snapshot (server.Server.Stats).
type Source interface {
	Stats() Stats
}

// Start serves the ops endpoints on addr and returns the bound
// listener (so callers can learn the port when addr uses :0):
//
//	/stats          — JSON snapshot of live server state (Stats)
//	/debug/pprof/*  — Go profiling (CPU, heap, goroutines, …)
//
// pprof can burn CPU on demand and dump memory, so the endpoint is never
// open to the network unauthenticated: with a token every request must
// carry "Authorization: Bearer <token>" (else 401); without one Start
// refuses any addr that isn't loopback.
func Start(src Source, addr, token string) (net.Listener, error) {
	if token == "" && !loopback(addr) {
		return nil, fmt.Errorf("metrics on non-loopback %q needs a token (METRICS_TOKEN)", addr)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(src.Stats())
	})

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}
	srv := &http.Server{Handler: requireToken(token, mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		// net.ErrClosed is the normal shutdown path (listener closed).
		if err := srv.Serve(lis); err != nil && !errors.Is(err, net.ErrClosed) {
			slog.Error("metrics server failed", "err", err)
		}
	}()
	return lis, nil
}

// requireToken lets a request through only with "Authorization: Bearer
// token" (compared in constant time); an empty token lets everything
// through.
func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopback reports whether addr (host:port) binds only the loopback
// interface. An empty host, 0.0.0.0 or :: listen everywhere.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
