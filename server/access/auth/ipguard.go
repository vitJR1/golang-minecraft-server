package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// IPGuard is the per-IP side of the gate. fails counts consecutive
// wrong-password attempts since the last success; cleared on successful
// login. bans holds the un-ban time per IP — read at every new connection's
// login handler, written when fails crosses the attempt limit.
//
// bans is persisted to path; fails is in-memory only (fail counts naturally
// reset on restart, which is the right behaviour — a flaky network shouldn't
// accumulate ban credit across hours).
//
// lastSeen maps lower-cased player name → IP address from the most recent
// login. Lets /banip and /unbanip take a nickname, not just an IP. Not
// persisted — a restart loses it; admins fall back to passing the literal
// IP if needed.
//
// Every method takes the IP as a string (see server's clientIP); "" means
// unknown and is never banned.
type IPGuard struct {
	path string

	mu       sync.Mutex
	fails    map[string]int
	bans     map[string]time.Time
	lastSeen map[string]string
	now      func() time.Time // injectable clock for tests
}

// NewIPGuard returns an empty guard persisting its bans to path. Call Load
// to read bans saved by a previous run.
func NewIPGuard(path string) *IPGuard {
	return &IPGuard{
		path:     path,
		fails:    map[string]int{},
		bans:     map[string]time.Time{},
		lastSeen: map[string]string{},
		now:      time.Now,
	}
}

// Path is the bans file.
func (g *IPGuard) Path() string { return g.path }

// Allowed returns the unban time + false if ip is currently banned for
// failed auth attempts, otherwise (zero, true). An expired ban is dropped
// along with the IP's fail count.
func (g *IPGuard) Allowed(ip string) (time.Time, bool) {
	if ip == "" {
		return time.Time{}, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	until, banned := g.bans[ip]
	if !banned {
		return time.Time{}, true
	}
	if g.now().After(until) {
		delete(g.bans, ip)
		delete(g.fails, ip)
		return time.Time{}, true
	}
	return until, false
}

// RecordFail bumps ip's wrong-password counter and, once it reaches
// maxAttempts, installs a ban for banFor. Returns true if the ban just
// engaged on this call. When that happens, the updated ban list is also
// flushed to disk (best-effort — a save failure is logged but doesn't
// prevent the in-memory ban).
func (g *IPGuard) RecordFail(ip string, maxAttempts int, banFor time.Duration) bool {
	if ip == "" {
		return false
	}
	g.mu.Lock()
	g.fails[ip]++
	banned := g.fails[ip] >= maxAttempts
	if banned {
		g.bans[ip] = g.now().Add(banFor)
	}
	g.mu.Unlock()
	if banned {
		if err := g.Save(); err != nil {
			slog.Warn("auth: ipbans save failed", "path", g.path, "err", err)
		}
	}
	return banned
}

// Fails is ip's current wrong-password count.
func (g *IPGuard) Fails(ip string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fails[ip]
}

// ClearFails wipes ip's failure counter after a successful login.
func (g *IPGuard) ClearFails(ip string) {
	if ip == "" {
		return
	}
	g.mu.Lock()
	delete(g.fails, ip)
	g.mu.Unlock()
}

// Remember records ip under the player's lower-cased name so a later
// /banip or /unbanip targeting that name can find it.
func (g *IPGuard) Remember(name, ip string) {
	if ip == "" {
		return
	}
	g.mu.Lock()
	g.lastSeen[strings.ToLower(name)] = ip
	g.mu.Unlock()
}

// Resolve turns a /banip /unbanip argument into an IP. Try as a player
// nickname first — that's the documented UX. If the argument doesn't match
// any known name, fall back to treating it as a literal IP (so admins can
// ban an address they pulled from logs even if no player ever joined from
// it).
func (g *IPGuard) Resolve(arg string) string {
	g.mu.Lock()
	ip, ok := g.lastSeen[strings.ToLower(arg)]
	g.mu.Unlock()
	if ok {
		return ip
	}
	return arg
}

// Ban adds a manual IP ban for duration and re-saves the bans file.
// Returns the absolute time the ban lifts.
func (g *IPGuard) Ban(ip string, duration time.Duration) time.Time {
	until := g.now().Add(duration)
	g.mu.Lock()
	g.bans[ip] = until
	g.mu.Unlock()
	if err := g.Save(); err != nil {
		slog.Warn("auth: ipbans save after manual ban failed", "path", g.path, "err", err)
	}
	return until
}

// Unban removes ip's ban and resets its fail counter, then re-saves the
// bans file. Returns true if the IP was banned (or had fails) before this
// call, false if it was already clean.
func (g *IPGuard) Unban(ip string) bool {
	g.mu.Lock()
	_, wasBanned := g.bans[ip]
	_, hadFails := g.fails[ip]
	delete(g.bans, ip)
	delete(g.fails, ip)
	g.mu.Unlock()
	if wasBanned {
		if err := g.Save(); err != nil {
			slog.Warn("auth: ipbans save after unban failed", "path", g.path, "err", err)
		}
	}
	return wasBanned || hadFails
}

// Load hydrates the bans from path. Expired entries are dropped on load so
// a server that's been off for hours doesn't keep stale bans. Missing file
// is fine — fresh install starts with no bans.
func (g *IPGuard) Load() error {
	data, err := os.ReadFile(g.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", g.path, err)
	}
	var loaded map[string]time.Time
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("parse %s: %w", g.path, err)
	}
	now := g.now()
	live := make(map[string]time.Time, len(loaded))
	for ip, until := range loaded {
		if until.After(now) {
			live[ip] = until
		}
	}
	g.mu.Lock()
	g.bans = live
	g.mu.Unlock()
	return nil
}

// Save atomically writes the current ban map to disk.
func (g *IPGuard) Save() error {
	g.mu.Lock()
	// Snapshot under the lock so concurrent writes can't tear the map.
	snap := make(map[string]time.Time, len(g.bans))
	for ip, until := range g.bans {
		snap[ip] = until
	}
	g.mu.Unlock()

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := g.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	return os.Rename(tmp, g.path)
}
