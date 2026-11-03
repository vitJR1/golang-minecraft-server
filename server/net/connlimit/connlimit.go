// Package connlimit caps pre-login connections: total, per IP and new
// connections per IP per window.
package connlimit

import (
	"minecraft-server/cfg"
	"net"
	"strings"
	"sync"
	"time"
)

// connRateWindow is the sliding window for cfg.ConnRatePerIP: at most that
// many NEW connections per IP are admitted within one window.
const connRateWindow = 10 * time.Second

// connSweepInterval is how often Acquire opportunistically prunes idle IP
// entries (no active connections, rate window expired) so the map doesn't
// grow with every IP that ever pinged the server.
const connSweepInterval = time.Minute

// Limiter enforces the pre-login connection caps: total connections,
// concurrent connections per IP, and new-connection rate per IP. It sits in
// front of everything (status pings included) — the point is bounding the
// sockets and goroutines a flood can create, before any protocol work.
//
// Limits are read from cfg at check time (cfg values are set once at
// startup; tests adjust them per-case). Zero disables the respective limit.
type Limiter struct {
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

func New() *Limiter {
	return &Limiter{
		now:   time.Now,
		perIP: make(map[string]*ipConnState),
	}
}

// Acquire admits or rejects a new connection from ip. On admit it returns a
// release func (call exactly once, when the connection is done) and "".
// On reject it returns nil and a short reason for the log line.
func (l *Limiter) Acquire(ip string) (release func(), reason string) {
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
func (l *Limiter) maybeSweep(now time.Time) {
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

// Key is the per-IP limits' key for a client address: an IPv4 address (an
// IPv4-mapped IPv6 one included) as itself, an IPv6 address as its /64 —
// one subscriber usually holds a whole /64, so keying by the full address
// let a single host open unlimited connections (and dodge IP bans) by
// rotating through it.
func Key(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	if len(ip) == net.IPv6len {
		return (&net.IPNet{IP: ip.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
	}
	return ip.String()
}

// KeyString is Key for an address in text (an admin's /banip argument, a
// saved ban); anything that doesn't parse as an IP is returned unchanged.
func KeyString(s string) string {
	if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
		return Key(ip)
	}
	return s
}
