// Package authgate is the offline-mode auth gate: AuthMe-style first-time
// registration + per-connection login. Online mode already verifies player
// identity via Mojang, so Enable should NOT be called when cfg.OnlineMode
// is true.
//
// Flow on offline-mode:
//
//  1. Player joins → routed to the dedicated `auth` instance (NOT
//     Hub). World is empty, build/break/PvP all vetoed.
//  2. Server prompts "/register <pass> <pass>" (first time) or
//     "/login <pass>" (returning).
//  3. Until they comply, chat is dropped + commands other than the
//     whitelist (register, login, help) are rejected.
//  4. cfg.AuthTimeout (default 30s) — if not authed by then, kick
//     with "Auth timed out".
//  5. cfg.AuthMaxAttempts (default 3) consecutive wrong passwords on
//     the same IP → IP banned for cfg.AuthBanDuration (default 1h);
//     future connections from that IP are dropped at login.
//  6. On success → MovePlayer to Hub (player sees Respawn + hub
//     chunks).
//
// Storage: password hashes (bcrypt) live in Postgres — the players table's
// password_hash column (auth.DBCredentials). When no database is configured
// the gate falls back to an in-memory store (auth.MemCredentials,
// non-persistent; also what the tests use). IP bans are a JSON sidecar file
// (auth.IPGuard, <path>.ipbans.json) — they're operational state, not user
// data. This package is the connection side over server.AuthGate; the
// logic is in server/access/auth.
package authgate

import (
	"fmt"
	"log/slog"
	"time"

	"minecraft-server/cfg"
	"minecraft-server/server"
	"minecraft-server/server/access/auth"
	"minecraft-server/world"
)

// Gate is a server's auth gate (server.AuthGate).
type Gate struct {
	srv *server.Server
	// svc holds the credentials (Postgres in production, in-memory
	// otherwise) and the per-IP fail counter / bans.
	svc *auth.Service
}

// Enable installs the offline-mode auth gate on s: creates the dedicated
// `auth` instance, wires its protection hooks, loads the IP bans from
// path + ".ipbans.json", and registers /register /login /banip /unbanip.
func Enable(s *server.Server, path string) *Gate {
	g := &Gate{
		srv: s,
		svc: &auth.Service{IPs: auth.NewIPGuard(path + ".ipbans.json")},
	}

	// Credentials live in Postgres when the store is up; otherwise fall back
	// to a non-persistent in-memory map so the gate still works (e.g. in dev
	// without a database, and in tests).
	credBackend := "postgres"
	if s.Store != nil {
		g.svc.Creds = auth.DBCredentials{Players: s.Store.Players}
	} else {
		g.svc.Creds = auth.NewMemCredentials()
		credBackend = "memory"
	}

	if err := g.svc.IPs.Load(); err != nil {
		slog.Warn("auth: failed to load IP bans", "path", g.svc.IPs.Path(), "err", err)
	}

	// Dedicated auth instance — players land here until they /login or
	// /register. With default gamemode now Adventure (no flight), we
	// drop a 7×7 stone pad one block below spawn (y=66) so they aren't
	// instantly falling into the void while typing their password.
	authWorld := world.NewMemoryWorld()
	for x := -3; x <= 3; x++ {
		for z := -3; z <= 3; z++ {
			authWorld.SetBlock(world.Position{X: x, Y: 66, Z: z}, world.Stone)
		}
	}
	authInst := server.NewInstance("auth", s, authWorld)
	authInst.OnBlockBreak = func(c *server.ClientConnection, _ world.Position) bool {
		_ = c.SendMessage("Please /login or /register first.")
		return false
	}
	authInst.OnBlockPlace = func(c *server.ClientConnection, _ world.Position, _ world.Block) bool {
		_ = c.SendMessage("Please /login or /register first.")
		return false
	}
	authInst.OnPlayerAttack = func(_, _ *server.ClientConnection) bool { return false }
	s.AddInstance(authInst)
	s.Auth = authInst
	s.SetAuthGate(g)

	registerCommands()
	slog.Info("auth: enabled",
		"credentials", credBackend,
		"timeout", cfg.AuthTimeout(),
		"max_attempts", cfg.AuthMaxAttempts(),
		"ban_duration", cfg.AuthBanDuration())
	return g
}

// of is the gate of c's server (nil when it has none).
func of(c *server.ClientConnection) *Gate {
	g, _ := c.Server().AuthGate().(*Gate)
	return g
}

// bypass lists slash-command names a non-authenticated player may run.
// Everything else gets the "/login first" rejection.
var bypass = map[string]struct{}{
	"register": {},
	"login":    {},
	"help":     {},
}

// Bypass implements server.AuthGate.
func (g *Gate) Bypass(name string) bool {
	_, ok := bypass[name]
	return ok
}

// IPBanned implements server.AuthGate: the unban time + true while ip is
// banned for failed auth attempts.
func (g *Gate) IPBanned(ip string) (time.Time, bool) {
	until, allowed := g.svc.IPs.Allowed(ip)
	return until, !allowed
}

// Joined implements server.AuthGate: tells the player how to authenticate
// and starts the timeout-kick goroutine.
func (g *Gate) Joined(c *server.ClientConnection) {
	if ip := c.IP(); ip != "" {
		g.svc.IPs.Remember(c.Name(), ip)
	}
	if g.svc.Registered(c.Name()) {
		_ = c.SendMessage("Welcome back. Please /login <password>")
	} else {
		_ = c.SendMessage("First visit — please /register <password> <password>")
	}
	go g.timeoutWatch(c)
}

// timeoutWatch sleeps in 1-second ticks, refreshing the XP-bar overlay
// to act as a visible countdown ("Auth: 28… 27… 26…") and kicking the
// connection when the deadline passes. Authentication wins → clear the
// bar via pushCountdownClear. cfg.AuthTimeout ≤ 0 disables the watch.
// Self-cancelling: a disconnect closes Done.
func (g *Gate) timeoutWatch(c *server.ClientConnection) {
	timeout := cfg.AuthTimeout()
	if timeout <= 0 {
		return
	}
	deadline := time.Now().Add(timeout)
	totalSeconds := float32(timeout.Seconds())

	pushCountdown(c, deadline, totalSeconds)

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-c.Done():
			return
		case <-tick.C:
		}
		if c.Authed() {
			pushCountdownClear(c)
			return
		}
		if c.Closed() {
			return
		}
		if !time.Now().Before(deadline) {
			slog.Info("auth: timeout, kicking",
				"player", c.Name(), "after", timeout)
			c.Disconnect(fmt.Sprintf(
				"Auth timed out (%s). Reconnect to try again.", timeout))
			return
		}
		pushCountdown(c, deadline, totalSeconds)
	}
}

// pushCountdown sends the current "seconds remaining" snapshot to the
// XP bar. Level = seconds left (round up so the first tick shows the
// full timeout), bar = fraction of remaining.
func pushCountdown(c *server.ClientConnection, deadline time.Time, totalSeconds float32) {
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	// Ceil-ish: bumps "29.4s left" to display 30 so the first second
	// matches the total and the last second is "1".
	secsLeft := int32(remaining.Seconds() + 0.999)
	bar := float32(remaining.Seconds()) / totalSeconds
	if bar < 0 {
		bar = 0
	} else if bar > 1 {
		bar = 1
	}
	_ = c.SetExperience(bar, secsLeft, 0)
}

// pushCountdownClear blanks the XP overlay so a freshly-authed player
// doesn't keep staring at a stale countdown number.
func pushCountdownClear(c *server.ClientConnection) {
	if c.Closed() {
		return
	}
	_ = c.SetExperience(0, 0, 0)
}
