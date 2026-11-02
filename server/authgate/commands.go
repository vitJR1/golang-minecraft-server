package authgate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"minecraft-server/cfg"
	"minecraft-server/server"
	"minecraft-server/server/auth"
	"minecraft-server/server/command"
	"minecraft-server/server/moderation"
)

// commands.go: /register /login (the gate itself) and the op-only /banip
// /unbanip. Registered by the first Enable; each looks up the gate of the
// caller's server.

var registerOnce sync.Once

func registerCommands() {
	registerOnce.Do(func() {
		players := command.ArgFrom(1, func(c *server.ClientConnection) []string { return c.Server().PlayerNames() })
		server.RegisterCommand(&server.Command{
			Name: "register",
			Help: "/register <password> <password> — first-time auth setup",
			Run:  cmdRegister,
		})
		server.RegisterCommand(&server.Command{
			Name: "login",
			Help: "/login <password> — sign in as a returning player",
			Run:  cmdLogin,
		})
		server.RegisterCommand(&server.Command{
			Name:     "banip",
			NeedsOp:  true,
			Help:     "/banip <player|ip> [duration] — ban IP (default 1h)",
			Run:      cmdBanIP,
			Complete: players, // player names suggested; an IP works too
		})
		server.RegisterCommand(&server.Command{
			Name:     "unbanip",
			NeedsOp:  true,
			Help:     "/unbanip <player|ip> — lift an auth IP ban + reset fails",
			Run:      cmdUnbanIP,
			Complete: players,
		})
	})
}

// cmdBanIP — /banip <name|ip> [duration]. Op-only. Resolves a
// player nickname to their last-seen IP (or accepts a raw address),
// adds the ban, persists it, and kicks any matching live sessions.
func cmdBanIP(c *server.ClientConnection, args []string) {
	g := of(c)
	if g == nil {
		_ = c.SendMessage("Auth plugin is disabled.")
		return
	}
	if len(args) < 1 || len(args) > 2 {
		_ = c.SendMessage("Usage: /banip <player|ip> [duration]")
		return
	}
	arg := strings.TrimSpace(args[0])
	if arg == "" {
		_ = c.SendMessage("Usage: /banip <player|ip> [duration]")
		return
	}
	ip := g.svc.IPs.Resolve(arg)
	duration := cfg.AuthBanDuration()
	if len(args) == 2 {
		d, err := moderation.ParseShortDuration(args[1])
		if err != nil {
			_ = c.SendMessage("Bad duration: " + err.Error())
			return
		}
		duration = d
	}
	if duration <= 0 {
		duration = cfg.AuthBanDuration()
	}
	until := g.svc.IPs.Ban(ip, duration)
	label := arg
	if ip != arg {
		label = arg + " (" + ip + ")"
	}
	_ = c.SendMessage(fmt.Sprintf("Banned %s until %s",
		label, until.Format("2006-01-02 15:04:05")))
	slog.Info("auth: manual IP ban",
		"arg", arg, "ip", ip, "until", until, "by", c.Name())

	// Kick any connected players from this IP so the ban takes effect
	// immediately instead of only on next reconnect.
	for _, conn := range g.srv.ConnectionsFromIP(ip) {
		conn.Kick(fmt.Sprintf("IP banned until %s", until.Format("2006-01-02 15:04:05")))
	}
}

// cmdUnbanIP — /unbanip <name|ip>. Op-only; clears the in-memory
// state AND rewrites auth.json.ipbans.json. Player nickname resolves
// to their last-seen IP if known; otherwise the arg is treated as a
// literal IP.
func cmdUnbanIP(c *server.ClientConnection, args []string) {
	g := of(c)
	if g == nil {
		_ = c.SendMessage("Auth plugin is disabled.")
		return
	}
	if len(args) != 1 {
		_ = c.SendMessage("Usage: /unbanip <player|ip>")
		return
	}
	arg := strings.TrimSpace(args[0])
	ip := g.svc.IPs.Resolve(arg)
	label := arg
	if ip != arg {
		label = arg + " (" + ip + ")"
	}
	if g.svc.IPs.Unban(ip) {
		_ = c.SendMessage("Cleared auth ban + fail counter for " + label)
		slog.Info("auth: unbanned IP", "arg", arg, "ip", ip, "by", c.Name())
	} else {
		_ = c.SendMessage("No active ban or failed attempts for " + label)
	}
}

// admit marks the player authed and moves them out of the auth instance
// into Hub. Runs from the command handler's readLoop goroutine — the only
// safe place to call Server.MovePlayer.
func (g *Gate) admit(c *server.ClientConnection, greeting string) {
	c.SetAuthed(true)
	pushCountdownClear(c)
	g.svc.IPs.ClearFails(c.IP())
	_ = c.SendMessage(greeting)
	if g.srv.Hub == nil {
		return
	}
	if _, err := g.srv.ToHub(c); err != nil {
		slog.Warn("auth: post-auth move to hub failed",
			"player", c.Name(), "err", err)
		_ = c.SendMessage("Couldn't enter hub: " + err.Error())
	}
}

// cmdRegister handles "/register <pass> <pass>".
func cmdRegister(c *server.ClientConnection, args []string) {
	g := of(c)
	if g == nil {
		_ = c.SendMessage("Auth is disabled on this server.")
		return
	}
	if c.Authed() {
		_ = c.SendMessage("You're already signed in.")
		return
	}
	if len(args) != 2 {
		_ = c.SendMessage("Usage: /register <password> <password>")
		return
	}
	if args[0] != args[1] {
		_ = c.SendMessage("Passwords don't match — try again.")
		return
	}
	if err := g.svc.Register(context.Background(), c.Name(), args[0]); err != nil {
		switch {
		case errors.Is(err, auth.ErrTooShort):
			_ = c.SendMessage(fmt.Sprintf("Password too short (min %d chars).", auth.MinPasswordLen))
		case errors.Is(err, auth.ErrRegistered):
			_ = c.SendMessage("Name already registered — use /login <password>.")
		default:
			slog.Error("auth: register failed", "player", c.Name(), "err", err)
			_ = c.SendMessage("Couldn't save credentials — try /register again.")
		}
		return
	}
	slog.Info("auth: registered", "player", c.Name())
	g.admit(c, "Registered. Welcome aboard.")
}

// cmdLogin handles "/login <pass>". Wrong-password failures get
// counted per-IP; cfg.AuthMaxAttempts of them in a row trips an IP ban
// for cfg.AuthBanDuration and disconnects the player immediately.
func cmdLogin(c *server.ClientConnection, args []string) {
	g := of(c)
	if g == nil {
		_ = c.SendMessage("Auth is disabled on this server.")
		return
	}
	if c.Authed() {
		_ = c.SendMessage("You're already signed in.")
		return
	}
	if len(args) != 1 {
		_ = c.SendMessage("Usage: /login <password>")
		return
	}

	switch err := g.svc.Verify(context.Background(), c.Name(), args[0]); {
	case errors.Is(err, auth.ErrNoAccount):
		_ = c.SendMessage("No account — use /register <password> <password>.")
		return
	case errors.Is(err, auth.ErrWrongPassword):
		ip := c.IP()
		if g.svc.IPs.RecordFail(ip, cfg.AuthMaxAttempts(), cfg.AuthBanDuration()) {
			slog.Warn("auth: IP banned for repeated failures",
				"player", c.Name(), "ip", ip,
				"duration", cfg.AuthBanDuration())
			c.Disconnect(fmt.Sprintf(
				"Too many wrong passwords. IP banned for %s.", cfg.AuthBanDuration()))
			return
		}
		_ = c.SendMessage("Wrong password.")
		slog.Info("auth: bad password", "player", c.Name())
		return
	case err != nil:
		slog.Error("auth: credential lookup failed", "player", c.Name(), "err", err)
		_ = c.SendMessage("Auth storage unavailable — try again shortly.")
		return
	}
	slog.Info("auth: login ok", "player", c.Name())
	g.admit(c, "Logged in. Welcome to the hub.")
}
