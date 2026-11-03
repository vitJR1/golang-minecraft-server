package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"minecraft-server/server/net/connlimit"
)

// IPGuard is the brute-force side of the gate: wrong-password counters and
// the IP bans they lead to.
//
// Failures are counted three ways, each counter forgotten once it has seen
// no failure for the ban duration:
//
//   - per (IP, account): maxAttempts of them ban the IP. A successful login
//     resets only the counter of the account that logged in — logging into
//     your own account between guesses at someone else's used to reset the
//     IP's whole count, which made the limit worthless.
//   - per IP over every account (password spraying): maxAttempts ×
//     ipSprayFactor ban the IP. Never reset by a success.
//   - per account over every IP (a guess spread across addresses): from
//     maxAttempts on, each further /login for that name has to wait an
//     exponentially growing delay (NameWait), capped at the ban duration.
//
// bans holds the un-ban time per IP — read at every new connection's login
// handler. bans is persisted to path; the counters are in-memory only (a
// restart forgets them, which is fine — a flaky network shouldn't
// accumulate ban credit across hours).
//
// lastSeen maps lower-cased player name → IP address from the most recent
// login. Lets /banip and /unbanip take a nickname, not just an IP. Not
// persisted — a restart loses it; admins fall back to passing the literal
// IP if needed.
//
// Every method takes the IP as a string (see server's ClientIP: an IPv6
// client is keyed by its /64, connlimit.Key); "" means unknown and is never
// banned.
type IPGuard struct {
	path string

	mu        sync.Mutex
	pairFails map[pairKey]failCount
	ipFails   map[string]failCount
	nameFails map[string]failCount
	lastSweep time.Time
	bans      map[string]time.Time
	lastSeen  map[string]string
	now       func() time.Time // injectable clock for tests

	// saveMu serialises Save from snapshot to rename, so an older snapshot
	// can never be renamed over a newer one.
	saveMu sync.Mutex
}

// ipSprayFactor: how many times maxAttempts failures one IP may make over
// all accounts together before it is banned.
const ipSprayFactor = 4

// pairKey is one (IP, lower-cased account name) counter.
type pairKey struct{ ip, name string }

// failCount is a counter with the time of its last failure.
type failCount struct {
	n    int
	last time.Time
}

// live is the count, or 0 once window has passed since the last failure.
func (f failCount) live(now time.Time, window time.Duration) int {
	if window > 0 && now.Sub(f.last) >= window {
		return 0
	}
	return f.n
}

// NewIPGuard returns an empty guard persisting its bans to path. Call Load
// to read bans saved by a previous run.
func NewIPGuard(path string) *IPGuard {
	return &IPGuard{
		path:      path,
		pairFails: map[pairKey]failCount{},
		ipFails:   map[string]failCount{},
		nameFails: map[string]failCount{},
		bans:      map[string]time.Time{},
		lastSeen:  map[string]string{},
		now:       time.Now,
	}
}

// Path is the bans file.
func (g *IPGuard) Path() string { return g.path }

// Allowed returns the unban time + false if ip is currently banned for
// failed auth attempts, otherwise (zero, true). An expired ban is dropped
// along with the IP's fail counts.
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
		g.forgetIPLocked(ip)
		return time.Time{}, true
	}
	return until, false
}

// forgetIPLocked drops every counter of ip. Called under g.mu.
func (g *IPGuard) forgetIPLocked(ip string) bool {
	_, had := g.ipFails[ip]
	delete(g.ipFails, ip)
	for k := range g.pairFails {
		if k.ip == ip {
			delete(g.pairFails, k)
			had = true
		}
	}
	return had
}

// RecordFail counts a wrong password for name from ip and, once ip has
// failed maxAttempts times for that name (or maxAttempts × ipSprayFactor
// times over all names), installs a ban for banFor. Returns true if the ban
// just engaged on this call; the ban list is then flushed to disk
// (best-effort — a save failure is logged but doesn't prevent the in-memory
// ban). The name's own counter (NameWait) is bumped even without an IP.
func (g *IPGuard) RecordFail(ip, name string, maxAttempts int, banFor time.Duration) bool {
	name = strings.ToLower(name)
	g.mu.Lock()
	now := g.now()
	g.sweepLocked(now, banFor)
	g.nameFails[name] = bump(g.nameFails[name], now, banFor)
	if ip == "" {
		g.mu.Unlock()
		return false
	}
	pk := pairKey{ip, name}
	g.pairFails[pk] = bump(g.pairFails[pk], now, banFor)
	g.ipFails[ip] = bump(g.ipFails[ip], now, banFor)
	banned := g.pairFails[pk].n >= maxAttempts || g.ipFails[ip].n >= maxAttempts*ipSprayFactor
	if banned {
		g.bans[ip] = now.Add(banFor)
	}
	g.mu.Unlock()
	if banned {
		if err := g.Save(); err != nil {
			slog.Warn("auth: ipbans save failed", "path", g.path, "err", err)
		}
	}
	return banned
}

// bump adds one failure at now to f (restarting it if it had expired).
func bump(f failCount, now time.Time, window time.Duration) failCount {
	return failCount{n: f.live(now, window) + 1, last: now}
}

// sweepLocked drops expired counters about once a minute, so addresses and
// names that failed once don't pile up. Called under g.mu.
func (g *IPGuard) sweepLocked(now time.Time, window time.Duration) {
	if now.Sub(g.lastSweep) < time.Minute {
		return
	}
	g.lastSweep = now
	for k, f := range g.pairFails {
		if f.live(now, window) == 0 {
			delete(g.pairFails, k)
		}
	}
	for k, f := range g.ipFails {
		if f.live(now, window) == 0 {
			delete(g.ipFails, k)
		}
	}
	for k, f := range g.nameFails {
		if f.live(now, window) == 0 {
			delete(g.nameFails, k)
		}
	}
}

// NameWait is how long a /login for name must still wait: zero below
// maxAttempts failures (over every IP), then 1 s after the last failure,
// doubling with each further one, capped at banFor.
func (g *IPGuard) NameWait(name string, maxAttempts int, banFor time.Duration) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	f := g.nameFails[strings.ToLower(name)]
	n := f.live(now, banFor)
	if n < maxAttempts {
		return 0
	}
	wait := banFor
	if shift := n - maxAttempts; shift < 32 {
		if d := time.Second << shift; d < wait {
			wait = d
		}
	}
	if left := f.last.Add(wait).Sub(now); left > 0 {
		return left
	}
	return 0
}

// Fails is the wrong-password count for name from ip.
func (g *IPGuard) Fails(ip, name string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pairFails[pairKey{ip, strings.ToLower(name)}].n
}

// ClearFails wipes the counters of the account name after a successful
// login from ip: its (ip, name) counter and its name-wide one. The IP's
// count over all accounts stays — a login to one's own account must not
// reset the guesses made at others.
func (g *IPGuard) ClearFails(ip, name string) {
	name = strings.ToLower(name)
	g.mu.Lock()
	delete(g.nameFails, name)
	delete(g.pairFails, pairKey{ip, name})
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

// Resolve turns a /banip /unbanip argument into an IP key. Try as a player
// nickname first — that's the documented UX. If the argument doesn't match
// any known name, fall back to treating it as a literal IP (so admins can
// ban an address they pulled from logs even if no player ever joined from
// it), keyed like a connection from it (an IPv6 address → its /64).
func (g *IPGuard) Resolve(arg string) string {
	g.mu.Lock()
	ip, ok := g.lastSeen[strings.ToLower(arg)]
	g.mu.Unlock()
	if ok {
		return ip
	}
	return connlimit.KeyString(arg)
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

// Unban removes ip's ban and resets its fail counters, then re-saves the
// bans file. Returns true if the IP was banned (or had fails) before this
// call, false if it was already clean.
func (g *IPGuard) Unban(ip string) bool {
	g.mu.Lock()
	_, wasBanned := g.bans[ip]
	delete(g.bans, ip)
	hadFails := g.forgetIPLocked(ip)
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
// is fine — fresh install starts with no bans. Keys are normalised like
// connections' (a full IPv6 address saved by an older build → its /64).
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
		ip = connlimit.KeyString(ip)
		if until.After(now) && until.After(live[ip]) {
			live[ip] = until
		}
	}
	g.mu.Lock()
	g.bans = live
	g.mu.Unlock()
	return nil
}

// Save atomically writes the current ban map to disk. Concurrent saves run
// one at a time, each from a snapshot taken after the previous one's, so
// the file always ends with the newest state.
func (g *IPGuard) Save() error {
	g.saveMu.Lock()
	defer g.saveMu.Unlock()

	g.mu.Lock()
	snap := make(map[string]time.Time, len(g.bans))
	for ip, until := range g.bans {
		snap[ip] = until
	}
	g.mu.Unlock()

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(g.path), filepath.Base(g.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), g.path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}
