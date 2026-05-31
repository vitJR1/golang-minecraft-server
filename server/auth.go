package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"minecraft-server/cfg"
	"minecraft-server/world"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost mirrors the vanilla server's bcrypt cost — 10 yields
// ~100ms per hash on a modern laptop, comfortable for a register/login
// gate that fires at most once per connection. Bump to 12 if you ever
// care about offline brute-force resistance more than register latency.
const bcryptCost = 10

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
// Storage: JSON file at the configured path, keyed by lower-cased name.
// Passwords are stored as SHA-256(salt || password) with a 16-byte
// per-user random salt — defeats rainbow tables; not bcrypt-strength
// but stdlib-only. Swap to argon2/bcrypt later if you care.

// authStore is the single live plugin instance. nil = auth disabled
// (default). Set by EnableAuth.
var authStore *authPlugin

type authPlugin struct {
	path     string
	bansPath string // sibling of path: derived as <path>.ipbans.json
	srv      *Server

	mu    sync.RWMutex
	creds map[string]*authCred // key = lowercase player name

	// IP failure tracking. ipFails counts consecutive wrong-password
	// attempts since the last success; cleared on successful login. ipBans
	// holds the un-ban time per IP — read at every new connection's
	// login handler, written when ipFails crosses cfg.AuthMaxAttempts.
	//
	// ipBans is persisted to bansPath; ipFails is in-memory only (fail
	// counts naturally reset on restart, which is the right behaviour —
	// a flaky network shouldn't accumulate ban credit across hours).
	ipMu    sync.Mutex
	ipFails map[string]int
	ipBans  map[string]time.Time

	// lastSeenIP maps lower-cased player name → IP address from the
	// most recent successful login. Lets /banip and /unbanip take a
	// nickname, not just an IP. Not persisted — server restart loses
	// this map; admins falls back to passing the literal IP if needed.
	lastSeenIP map[string]string
}

// authCred is the on-disk credential record. New entries use bcrypt and
// leave Salt empty (Hash is the full bcrypt string "$2a$10$…"). Legacy
// SHA-256 entries — written by an earlier version of this plugin —
// have both Salt + Hash set; they still verify and get auto-migrated
// to bcrypt on the next successful /login.
type authCred struct {
	Salt         string    `json:"salt,omitempty"` // legacy SHA-256 salt; empty for bcrypt
	Hash         string    `json:"hash"`           // bcrypt string OR base64(SHA-256(salt||pw))
	RegisteredAt time.Time `json:"registered_at"`
}

// isLegacySHA reports whether the credential is the older SHA-256
// format (which kept Salt populated). bcrypt strings always begin
// with "$2" so the cheap-and-cheerful test is: salt set => legacy.
func (c *authCred) isLegacySHA() bool { return c.Salt != "" }

// EnableAuth installs the offline-mode auth gate: creates the dedicated
// `auth` instance, wires its protection hooks, loads credentials from
// path, and registers the /register and /login commands.
//
// If path doesn't exist yet, the store starts empty — every connecting
// player will be prompted to /register.
func EnableAuth(s *Server, path string) {
	p := &authPlugin{
		path:       path,
		bansPath:   path + ".ipbans.json",
		srv:        s,
		creds:      map[string]*authCred{},
		ipFails:    map[string]int{},
		ipBans:     map[string]time.Time{},
		lastSeenIP: map[string]string{},
	}
	if err := p.load(); err != nil {
		slog.Warn("auth: failed to load credentials", "path", path, "err", err)
	}
	if err := p.loadBans(); err != nil {
		slog.Warn("auth: failed to load IP bans", "path", p.bansPath, "err", err)
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
		"credentials", path,
		"registered_count", len(p.creds),
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
	authStore.rememberIP(c)
	c.authed.Store(false)
	if authStore.has(c.playerName) {
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
	ip := clientIP(addr)
	if ip == "" {
		return time.Time{}, true
	}
	p.ipMu.Lock()
	defer p.ipMu.Unlock()
	until, banned := p.ipBans[ip]
	if !banned {
		return time.Time{}, true
	}
	if time.Now().After(until) {
		delete(p.ipBans, ip)
		delete(p.ipFails, ip)
		return time.Time{}, true
	}
	return until, false
}

// recordFail bumps the per-IP wrong-password counter and, if it crosses
// cfg.AuthMaxAttempts, installs an IP ban for cfg.AuthBanDuration.
// Returns true if the ban just engaged on this call. When that happens,
// the updated ban list is also flushed to disk (best-effort — a save
// failure is logged but doesn't prevent the in-memory ban).
func (p *authPlugin) recordFail(addr net.Addr) bool {
	ip := clientIP(addr)
	if ip == "" {
		return false
	}
	p.ipMu.Lock()
	p.ipFails[ip]++
	banned := p.ipFails[ip] >= cfg.AuthMaxAttempts()
	if banned {
		p.ipBans[ip] = time.Now().Add(cfg.AuthBanDuration())
	}
	p.ipMu.Unlock()
	if banned {
		if err := p.saveBans(); err != nil {
			slog.Warn("auth: ipbans save failed",
				"path", p.bansPath, "err", err)
		}
	}
	return banned
}

// clearFails wipes the IP's failure counter after a successful login.
func (p *authPlugin) clearFails(addr net.Addr) {
	ip := clientIP(addr)
	if ip == "" {
		return
	}
	p.ipMu.Lock()
	delete(p.ipFails, ip)
	p.ipMu.Unlock()
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

// has reports whether playerName has a credential entry.
func (p *authPlugin) has(name string) bool {
	p.mu.RLock()
	_, ok := p.creds[strings.ToLower(name)]
	p.mu.RUnlock()
	return ok
}

// bcryptHash wraps bcrypt.GenerateFromPassword and returns the result as
// a string ready for storage. Errors propagate so the caller can show a
// meaningful message — generation only fails on bogus cost values, which
// shouldn't happen with our hardcoded const.
func bcryptHash(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// verifyCred returns true if password matches the stored credential.
// Picks the right algorithm based on whether the legacy SHA-256 salt is
// present. Constant-time compare is built into bcrypt; the legacy SHA
// path uses a plain string compare on hex output (still constant-time
// in practice since both sides are fixed-length base64).
func verifyCred(cred *authCred, password string) bool {
	if cred.isLegacySHA() {
		salt, err := base64.StdEncoding.DecodeString(cred.Salt)
		if err != nil {
			return false
		}
		return hashLegacySHA(salt, password) == cred.Hash
	}
	return bcrypt.CompareHashAndPassword([]byte(cred.Hash), []byte(password)) == nil
}

// hashLegacySHA is the original base64(SHA-256(salt || password)) used
// before the bcrypt migration. Kept only for verifyCred's legacy
// branch — never write new entries this way.
func hashLegacySHA(salt []byte, password string) string {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// load reads the credentials file. Missing file is fine — store stays
// empty and every player will get the register prompt.
func (p *authPlugin) load() error {
	data, err := os.ReadFile(p.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", p.path, err)
	}
	var loaded map[string]*authCred
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("parse %s: %w", p.path, err)
	}
	norm := make(map[string]*authCred, len(loaded))
	for k, v := range loaded {
		norm[strings.ToLower(k)] = v
	}
	p.mu.Lock()
	p.creds = norm
	p.mu.Unlock()
	return nil
}

// save atomically writes the credential map back to disk.
func (p *authPlugin) save() error {
	p.mu.RLock()
	data, err := json.MarshalIndent(p.creds, "", "  ")
	p.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	return os.Rename(tmp, p.path)
}

// loadBans hydrates ipBans from bansPath. Expired entries are dropped on
// load so a server that's been off for hours doesn't keep stale bans.
// Missing file is fine — fresh install starts with no bans.
func (p *authPlugin) loadBans() error {
	data, err := os.ReadFile(p.bansPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", p.bansPath, err)
	}
	var loaded map[string]time.Time
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("parse %s: %w", p.bansPath, err)
	}
	now := time.Now()
	live := make(map[string]time.Time, len(loaded))
	for ip, until := range loaded {
		if until.After(now) {
			live[ip] = until
		}
	}
	p.ipMu.Lock()
	p.ipBans = live
	p.ipMu.Unlock()
	return nil
}

// saveBans atomically writes the current IP-ban map to disk. Called
// after every newly-engaged ban from recordFail.
func (p *authPlugin) saveBans() error {
	p.ipMu.Lock()
	// Snapshot under the lock so concurrent writes can't tear the map.
	snap := make(map[string]time.Time, len(p.ipBans))
	for ip, until := range p.ipBans {
		snap[ip] = until
	}
	p.ipMu.Unlock()

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := p.bansPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	return os.Rename(tmp, p.bansPath)
}

// rememberIP records the player's IP under their lower-cased name so a
// later /banip or /unbanip targeting that name can find the IP. Called
// from promptAuth on every login.
func (p *authPlugin) rememberIP(c *ClientConnection) {
	if c == nil || c.conn == nil {
		return
	}
	ip := clientIP(c.conn.RemoteAddr())
	if ip == "" {
		return
	}
	p.ipMu.Lock()
	p.lastSeenIP[strings.ToLower(c.playerName)] = ip
	p.ipMu.Unlock()
}

// resolveIPArg turns a /banip /unbanip argument into an IP. Try as a
// player nickname first via lastSeenIP — that's the documented UX. If
// the argument doesn't match any known name, fall back to treating it
// as a literal IP (so admins can ban an address they pulled from logs
// even if no player ever joined from it).
func (p *authPlugin) resolveIPArg(arg string) string {
	p.ipMu.Lock()
	ip, ok := p.lastSeenIP[strings.ToLower(arg)]
	p.ipMu.Unlock()
	if ok {
		return ip
	}
	return arg
}

// BanIP adds a manual IP ban with the given duration and re-saves the
// bans file. duration ≤ 0 is treated as "use cfg.AuthBanDuration".
// Returns the absolute time the ban lifts.
func (p *authPlugin) BanIP(ip string, duration time.Duration) time.Time {
	if duration <= 0 {
		duration = cfg.AuthBanDuration()
	}
	until := time.Now().Add(duration)
	p.ipMu.Lock()
	p.ipBans[ip] = until
	p.ipMu.Unlock()
	if err := p.saveBans(); err != nil {
		slog.Warn("auth: ipbans save after manual ban failed",
			"path", p.bansPath, "err", err)
	}
	return until
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
	ip := authStore.resolveIPArg(arg)
	duration := cfg.AuthBanDuration()
	if len(args) == 2 {
		d, err := ParseShortDuration(args[1])
		if err != nil {
			_ = c.sendSystemMessage("Bad duration: " + err.Error())
			return
		}
		duration = d
	}
	until := authStore.BanIP(ip, duration)
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

// UnbanIP removes the auth-plugin IP ban + resets the fail counter for
// that IP, then re-saves the bans file. Exposed for /unbanip and for
// admin tooling. Returns true if the IP was banned (or had fails)
// before this call, false if it was already clean.
func (p *authPlugin) UnbanIP(ip string) bool {
	p.ipMu.Lock()
	_, wasBanned := p.ipBans[ip]
	_, hadFails := p.ipFails[ip]
	delete(p.ipBans, ip)
	delete(p.ipFails, ip)
	p.ipMu.Unlock()
	if wasBanned {
		if err := p.saveBans(); err != nil {
			slog.Warn("auth: ipbans save after unban failed",
				"path", p.bansPath, "err", err)
		}
	}
	return wasBanned || hadFails
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
	ip := authStore.resolveIPArg(arg)
	label := arg
	if ip != arg {
		label = arg + " (" + ip + ")"
	}
	if authStore.UnbanIP(ip) {
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
	if len(args[0]) < 4 {
		_ = c.sendSystemMessage("Password too short (min 4 chars).")
		return
	}
	if authStore.has(c.playerName) {
		_ = c.sendSystemMessage("Name already registered — use /login <password>.")
		return
	}

	hash, err := bcryptHash(args[0])
	if err != nil {
		_ = c.sendSystemMessage("Internal error hashing password.")
		slog.Error("auth: bcrypt failed", "err", err)
		return
	}
	cred := &authCred{
		Hash:         hash,
		RegisteredAt: time.Now(),
	}
	authStore.mu.Lock()
	authStore.creds[strings.ToLower(c.playerName)] = cred
	authStore.mu.Unlock()

	if err := authStore.save(); err != nil {
		slog.Error("auth: save failed", "path", authStore.path, "err", err)
		_ = c.sendSystemMessage("Couldn't save credentials — try /register again.")
		return
	}

	c.authed.Store(true)
	pushCountdownClear(c)
	authStore.clearFails(c.conn.RemoteAddr())
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

	authStore.mu.RLock()
	cred, ok := authStore.creds[strings.ToLower(c.playerName)]
	authStore.mu.RUnlock()
	if !ok {
		_ = c.sendSystemMessage("No account — use /register <password> <password>.")
		return
	}

	if !verifyCred(cred, args[0]) {
		addr := c.conn.RemoteAddr()
		banned := authStore.recordFail(addr)
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
	}

	// Successful login on a legacy SHA-256 entry → upgrade to bcrypt
	// transparently. We have the plaintext password right here; this is
	// the one moment we can re-hash it correctly. Failure is non-fatal —
	// the login still succeeds, the migration just happens next time.
	if cred.isLegacySHA() {
		if newHash, err := bcryptHash(args[0]); err == nil {
			authStore.mu.Lock()
			cred.Hash = newHash
			cred.Salt = ""
			authStore.mu.Unlock()
			if err := authStore.save(); err != nil {
				slog.Warn("auth: post-login bcrypt migration save failed",
					"player", c.playerName, "err", err)
			} else {
				slog.Info("auth: migrated to bcrypt", "player", c.playerName)
			}
		}
	}

	c.authed.Store(true)
	pushCountdownClear(c)
	authStore.clearFails(c.conn.RemoteAddr())
	_ = c.sendSystemMessage("Logged in. Welcome to the hub.")
	slog.Info("auth: login ok", "player", c.playerName)
	teleportToHub(c)
}
