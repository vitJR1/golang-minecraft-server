package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"minecraft-server/cfg"
	"minecraft-server/server/auth"
	"minecraft-server/server/moderation"
	"minecraft-server/world"
)

// Auth plugin: AuthMe-style first-time registration + per-connection
// login for offline-mode servers. Online mode already verifies player
// identity via Mojang, so EnableAuth should NOT be called when
// cfg.OnlineMode is true.
//
// Flow on offline-mode:
//
//	1. Player joins → routed to the dedicated `auth` instance (NOT
//	   Hub). World is empty, build/break/PvP all vetoed.
//	2. Server prompts "/register <pass> <pass>" (first time) or
//	   "/login <pass>" (returning).
//	3. Until they comply, chat is dropped + commands other than the
//	   whitelist (register, login, hub, help) are rejected.
//	4. cfg.AuthTimeout (default 30s) — if not authed by then, kick
//	   with "Auth timed out".
//	5. cfg.AuthMaxAttempts (default 3) consecutive wrong passwords on
//	   the same IP → IP banned for cfg.AuthBanDuration (default 1h);
//	   future connections from that IP are dropped at handshake.
//	6. On success → MovePlayer to Hub (player sees Respawn + hub
//	   chunks).
//
// Storage: password hashes (bcrypt) live in Postgres — the players table's
// password_hash column (auth.DBCredentials). When no database is configured
// the plugin falls back to an in-memory store (auth.MemCredentials,
// non-persistent; also what the tests use). IP bans are a JSON sidecar file
// (auth.IPGuard, <path>.ipbans.json) — they're operational state, not user
// data. This file is the connection side; the logic is in server/auth.

// authStore is the single live plugin instance. nil = auth disabled
// (default). Set by EnableAuth.
var authStore *authPlugin

type authPlugin struct {
	path string
	srv  *Server
	// svc holds the credentials (Postgres in production, in-memory
	// otherwise) and the per-IP fail counter / bans.
	svc *auth.Service
}

// EnableAuth installs the offline-mode auth gate: creates the dedicated
// `auth` instance, wires its protection hooks, loads credentials from
// path, and registers the /register and /login commands.
//
// If path doesn't exist yet, the store starts empty — every connecting
// player will be prompted to /register.
func EnableAuth(s *Server, path string) {
	p := &authPlugin{
		path: path,
		srv:  s,
		svc:  &auth.Service{IPs: auth.NewIPGuard(path + ".ipbans.json")},
	}

	// Credentials live in Postgres when the store is up; otherwise fall back
	// to a non-persistent in-memory map so the gate still works (e.g. in dev
	// without a database, and in tests).
	credBackend := "postgres"
	if s.Store != nil {
		p.svc.Creds = auth.DBCredentials{Players: s.Store.Players}
	} else {
		p.svc.Creds = auth.NewMemCredentials()
		credBackend = "memory"
	}

	if err := p.svc.IPs.Load(); err != nil {
		slog.Warn("auth: failed to load IP bans", "path", p.svc.IPs.Path(), "err", err)
	}
	authStore = p

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
	authInst := NewInstance("auth", s, authWorld)
	authInst.OnBlockBreak = func(c *ClientConnection, _ world.Position) bool {
		_ = c.sendSystemMessage("Please /login or /register first.")
		return false
	}
	authInst.OnBlockPlace = func(c *ClientConnection, _ world.Position, _ world.Block) bool {
		_ = c.sendSystemMessage("Please /login or /register first.")
		return false
	}
	authInst.OnPlayerAttack = func(_, _ *ClientConnection) bool { return false }
	s.AddInstance(authInst)
	s.Auth = authInst

	registerCommand(&Command{
		Name:    "register",
		NeedsOp: false,
		Help:    "/register <password> <password> — first-time auth setup",
		Run:     cmdAuthRegister,
	})
	registerCommand(&Command{
		Name:    "login",
		NeedsOp: false,
		Help:    "/login <password> — sign in as a returning player",
		Run:     cmdAuthLogin,
	})
	registerCommand(&Command{
		Name:    "banip",
		NeedsOp: true,
		Help:    "/banip <player|ip> [duration] — ban IP (default 1h)",
		Run:     cmdAuthBanIP,
	})
	registerCommand(&Command{
		Name:    "unbanip",
		NeedsOp: true,
		Help:    "/unbanip <player|ip> — lift an auth IP ban + reset fails",
		Run:     cmdAuthUnbanIP,
	})
	slog.Info("auth: enabled",
		"credentials", credBackend,
		"timeout", cfg.AuthTimeout(),
		"max_attempts", cfg.AuthMaxAttempts(),
		"ban_duration", cfg.AuthBanDuration())
}

// authBypassCommands lists slash-command names a non-authenticated
// player may run. Everything else gets the "/login first" rejection.
var authBypassCommands = map[string]struct{}{
	"register": {},
	"login":    {},
	"help":     {},
}

// gateAuth is the per-action enforcement. Returns true if the action is
// allowed (auth disabled or player already authed), false if the player
// should be blocked.
func gateAuth(c *ClientConnection, reason string) bool {
	if authStore == nil || c.authed.Load() {
		return true
	}
	_ = c.sendSystemMessage(reason)
	return false
}

// promptAuth runs in handler_login after JoinAndAnnounce. Marks the
// connection unauthed, tells the player how to authenticate, and starts
// the timeout-kick goroutine. No-op if auth is disabled.
func promptAuth(c *ClientConnection) {
	if authStore == nil {
		return
	}
	if c.conn != nil {
		authStore.svc.IPs.Remember(c.playerName, clientIP(c.conn.RemoteAddr()))
	}
	c.authed.Store(false)
	if authStore.svc.Registered(c.playerName) {
		_ = c.sendSystemMessage("Welcome back. Please /login <password>")
	} else {
		_ = c.sendSystemMessage("First visit — please /register <password> <password>")
	}
	go authStore.timeoutWatch(c)
}

// timeoutWatch sleeps cfg.AuthTimeout then kicks the connection if it
// still isn't authed. Cheap and self-cancelling: the player getting
// authed makes the check a no-op; a disconnect closes the conn and
// makes sendPlayDisconnect / cleanup harmless on the second call.
// timeoutWatch sleeps in 1-second ticks, refreshing the XP-bar overlay
// to act as a visible countdown ("Auth: 28… 27… 26…") and kicking the
// connection when the deadline passes. Authentication wins → clear the
// bar via pushCountdownClear. cfg.AuthTimeout ≤ 0 disables the watch.
func (p *authPlugin) timeoutWatch(c *ClientConnection) {
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
		case <-c.done:
			return
		case <-tick.C:
		}
		if c.authed.Load() {
			pushCountdownClear(c)
			return
		}
		if c.isClosed() {
			return
		}
		if !time.Now().Before(deadline) {
			slog.Info("auth: timeout, kicking",
				"player", c.playerName, "after", timeout)
			_ = c.sendPlayDisconnect(fmt.Sprintf(
				"Auth timed out (%s). Reconnect to try again.", timeout))
			c.cleanup()
			return
		}
		pushCountdown(c, deadline, totalSeconds)
	}
}

// pushCountdown sends the current "seconds remaining" snapshot to the
// XP bar. Level = seconds left (round up so the first tick shows the
// full timeout), bar = fraction of remaining.
func pushCountdown(c *ClientConnection, deadline time.Time, totalSeconds float32) {
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
	_ = c.sendExperience(bar, secsLeft, 0)
}

// pushCountdownClear blanks the XP overlay so a freshly-authed player
// doesn't keep staring at a stale countdown number.
func pushCountdownClear(c *ClientConnection) {
	if c.isClosed() {
		return
	}
	_ = c.sendExperience(0, 0, 0)
}

// CheckIPAllowed returns the unban time + false if the address is
// currently banned for failed auth attempts, otherwise (zero, true).
// Used by handler_login at the start of the login flow.
func (p *authPlugin) CheckIPAllowed(addr net.Addr) (time.Time, bool) {
	return p.svc.IPs.Allowed(clientIP(addr))
}

// clientIP extracts the IP portion of a net.Addr. Works for *net.TCPAddr
// (the production case) and falls back to addr.String() for net.Pipe
// connections used in tests.
func clientIP(addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.String()
	}
	if addr == nil {
		return ""
	}
	return addr.String()
}

// cmdAuthBanIP — /banip <name|ip> [duration]. Op-only. Resolves a
// player nickname to their last-seen IP (or accepts a raw address),
// adds the ban, persists it, and kicks any matching live sessions.
func cmdAuthBanIP(c *ClientConnection, args []string) {
	if authStore == nil {
		_ = c.sendSystemMessage("Auth plugin is disabled.")
		return
	}
	if len(args) < 1 || len(args) > 2 {
		_ = c.sendSystemMessage("Usage: /banip <player|ip> [duration]")
		return
	}
	arg := strings.TrimSpace(args[0])
	if arg == "" {
		_ = c.sendSystemMessage("Usage: /banip <player|ip> [duration]")
		return
	}
	ip := authStore.svc.IPs.Resolve(arg)
	duration := cfg.AuthBanDuration()
	if len(args) == 2 {
		d, err := moderation.ParseShortDuration(args[1])
		if err != nil {
			_ = c.sendSystemMessage("Bad duration: " + err.Error())
			return
		}
		duration = d
	}
	if duration <= 0 {
		duration = cfg.AuthBanDuration()
	}
	until := authStore.svc.IPs.Ban(ip, duration)
	label := arg
	if ip != arg {
		label = arg + " (" + ip + ")"
	}
	_ = c.sendSystemMessage(fmt.Sprintf("Banned %s until %s",
		label, until.Format("2006-01-02 15:04:05")))
	slog.Info("auth: manual IP ban",
		"arg", arg, "ip", ip, "until", until, "by", c.playerName)

	// Kick any connected players from this IP so the ban takes effect
	// immediately instead of only on next reconnect.
	for _, conn := range c.server.connectionsFromIP(ip) {
		_ = conn.sendPlayDisconnect(fmt.Sprintf(
			"IP banned until %s", until.Format("2006-01-02 15:04:05")))
		go conn.cleanup()
	}
}

// cmdAuthUnbanIP — /unbanip <name|ip>. Op-only; clears the in-memory
// state AND rewrites auth.json.ipbans.json. Player nickname resolves
// to their last-seen IP if known; otherwise the arg is treated as a
// literal IP.
func cmdAuthUnbanIP(c *ClientConnection, args []string) {
	if authStore == nil {
		_ = c.sendSystemMessage("Auth plugin is disabled.")
		return
	}
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /unbanip <player|ip>")
		return
	}
	arg := strings.TrimSpace(args[0])
	ip := authStore.svc.IPs.Resolve(arg)
	label := arg
	if ip != arg {
		label = arg + " (" + ip + ")"
	}
	if authStore.svc.IPs.Unban(ip) {
		_ = c.sendSystemMessage("Cleared auth ban + fail counter for " + label)
		slog.Info("auth: unbanned IP", "arg", arg, "ip", ip, "by", c.playerName)
	} else {
		_ = c.sendSystemMessage("No active ban or failed attempts for " + label)
	}
}

// teleportToHub moves the just-authed player out of the auth instance
// and into Hub. Runs from the command handler's readLoop goroutine — the
// only safe place to call Server.MovePlayer.
func teleportToHub(c *ClientConnection) {
	if c.server == nil || c.server.Hub == nil {
		return
	}
	if c.instance == c.server.Hub {
		return // already there (auth disabled corner case)
	}
	if err := c.server.MovePlayer(c, c.server.Hub, 0.5, 67, 0.5); err != nil {
		slog.Warn("auth: post-auth move to hub failed",
			"player", c.playerName, "err", err)
		_ = c.sendSystemMessage("Couldn't enter hub: " + err.Error())
	}
}

// cmdAuthRegister handles "/register <pass> <pass>".
func cmdAuthRegister(c *ClientConnection, args []string) {
	if authStore == nil {
		_ = c.sendSystemMessage("Auth is disabled on this server.")
		return
	}
	if c.authed.Load() {
		_ = c.sendSystemMessage("You're already signed in.")
		return
	}
	if len(args) != 2 {
		_ = c.sendSystemMessage("Usage: /register <password> <password>")
		return
	}
	if args[0] != args[1] {
		_ = c.sendSystemMessage("Passwords don't match — try again.")
		return
	}
	if err := authStore.svc.Register(context.Background(), c.playerName, args[0]); err != nil {
		switch {
		case errors.Is(err, auth.ErrTooShort):
			_ = c.sendSystemMessage(fmt.Sprintf("Password too short (min %d chars).", auth.MinPasswordLen))
		case errors.Is(err, auth.ErrRegistered):
			_ = c.sendSystemMessage("Name already registered — use /login <password>.")
		default:
			slog.Error("auth: register failed", "player", c.playerName, "err", err)
			_ = c.sendSystemMessage("Couldn't save credentials — try /register again.")
		}
		return
	}

	c.authed.Store(true)
	pushCountdownClear(c)
	authStore.svc.IPs.ClearFails(clientIP(c.conn.RemoteAddr()))
	_ = c.sendSystemMessage("Registered. Welcome aboard.")
	slog.Info("auth: registered", "player", c.playerName)
	teleportToHub(c)
}

// cmdAuthLogin handles "/login <pass>". Wrong-password failures get
// counted per-IP; cfg.AuthMaxAttempts of them in a row trips an IP ban
// for cfg.AuthBanDuration and disconnects the player immediately.
func cmdAuthLogin(c *ClientConnection, args []string) {
	if authStore == nil {
		_ = c.sendSystemMessage("Auth is disabled on this server.")
		return
	}
	if c.authed.Load() {
		_ = c.sendSystemMessage("You're already signed in.")
		return
	}
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /login <password>")
		return
	}

	switch err := authStore.svc.Verify(context.Background(), c.playerName, args[0]); {
	case errors.Is(err, auth.ErrNoAccount):
		_ = c.sendSystemMessage("No account — use /register <password> <password>.")
		return
	case errors.Is(err, auth.ErrWrongPassword):
		addr := c.conn.RemoteAddr()
		banned := authStore.svc.IPs.RecordFail(clientIP(addr), cfg.AuthMaxAttempts(), cfg.AuthBanDuration())
		if banned {
			slog.Warn("auth: IP banned for repeated failures",
				"player", c.playerName, "ip", clientIP(addr),
				"duration", cfg.AuthBanDuration())
			_ = c.sendPlayDisconnect(fmt.Sprintf(
				"Too many wrong passwords. IP banned for %s.", cfg.AuthBanDuration()))
			c.cleanup()
			return
		}
		_ = c.sendSystemMessage("Wrong password.")
		slog.Info("auth: bad password", "player", c.playerName)
		return
	case err != nil:
		slog.Error("auth: credential lookup failed", "player", c.playerName, "err", err)
		_ = c.sendSystemMessage("Auth storage unavailable — try again shortly.")
		return
	}

	c.authed.Store(true)
	pushCountdownClear(c)
	authStore.svc.IPs.ClearFails(clientIP(c.conn.RemoteAddr()))
	_ = c.sendSystemMessage("Logged in. Welcome to the hub.")
	slog.Info("auth: login ok", "player", c.playerName)
	teleportToHub(c)
}
