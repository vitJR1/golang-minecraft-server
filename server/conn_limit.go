package server

import (
	"minecraft-server/cfg"
	"sync"
	"time"
)

// connRateWindow is the sliding window for cfg.ConnRatePerIP: at most that
// many NEW connections per IP are admitted within one window.
const connRateWindow = 10 * time.Second

// connSweepInterval is how often acquire opportunistically prunes idle IP
// entries (no active connections, rate window expired) so the map doesn't
// grow with every IP that ever pinged the server.
const connSweepInterval = time.Minute

// connLimiter enforces the pre-login connection caps: total connections,
// concurrent connections per IP, and new-connection rate per IP. It sits in
// front of everything (status pings included) — the point is bounding the
// sockets and goroutines a flood can create, before any protocol work.
//
// Limits are read from cfg at check time (cfg values are set once at
// startup; tests adjust them per-case). Zero disables the respective limit.
type connLimiter struct {
	mu        sync.Mutex
	now       func() time.Time // injectable clock for tests
	total     int
	perIP     map[string]*ipConnState
	lastSweep time.Time
}

// ipConnState tracks one IP: how many connections it holds open now, and how
// many it opened in the current rate window.
type ipConnState struct {
	active      int
	windowStart time.Time
	windowCount int
}

func newConnLimiter() *connLimiter {
	return &connLimiter{
		now:   time.Now,
		perIP: make(map[string]*ipConnState),
	}
}

// acquire admits or rejects a new connection from ip. On admit it returns a
// release func (call exactly once, when the connection is done) and "".
// On reject it returns nil and a short reason for the log line.
func (l *connLimiter) acquire(ip string) (release func(), reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.maybeSweep(now)

	if cfg.MaxConns > 0 && l.total >= cfg.MaxConns {
		return nil, "server connection limit"
	}

	st := l.perIP[ip]
	if st == nil {
		st = &ipConnState{windowStart: now}
		l.perIP[ip] = st
	}
	if now.Sub(st.windowStart) >= connRateWindow {
		st.windowStart = now
		st.windowCount = 0
	}
	if cfg.MaxConnsPerIP > 0 && st.active >= cfg.MaxConnsPerIP {
		return nil, "per-IP connection limit"
	}
	if cfg.ConnRatePerIP > 0 && st.windowCount >= cfg.ConnRatePerIP {
		return nil, "per-IP connection rate"
	}

	st.active++
	st.windowCount++
	l.total++

	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.total--
		st.active--
		// The entry stays until its rate window expires (dropping it now
		// would reset the counter and defeat the rate limit); the sweep
		// collects it once it's idle AND expired.
	}, ""
}

// maybeSweep drops idle, window-expired IP entries. Called under l.mu.
func (l *connLimiter) maybeSweep(now time.Time) {
	if now.Sub(l.lastSweep) < connSweepInterval {
		return
	}
	l.lastSweep = now
	for ip, st := range l.perIP {
		if st.active == 0 && now.Sub(st.windowStart) >= connRateWindow {
			delete(l.perIP, ip)
		}
	}
}
