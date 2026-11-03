package server

import (
	"net"
	"time"

	"minecraft-server/server/net/connlimit"
)

// auth_gate.go: the hook the offline-mode auth gate (server/access/authgate) plugs
// into. While a gate is set, players log into s.Auth unauthenticated: chat
// is dropped and only the commands the gate lets through run until it calls
// SetAuthed(true).

// AuthGate is installed with Server.SetAuthGate (before the server accepts
// connections).
type AuthGate interface {
	// IPBanned is asked at Login Start, before anything expensive (Mojang,
	// the ban list), so spammers are bounced fast.
	IPBanned(ip string) (until time.Time, banned bool)
	// Joined runs on the connection's readLoop once the unauthenticated
	// player has joined its landing instance.
	Joined(c *ClientConnection)
	// Bypass reports whether an unauthenticated player may run /name.
	Bypass(name string) bool
}

// SetAuthGate turns the gate on (nil turns it off); AuthGate is the one set.
func (s *Server) SetAuthGate(g AuthGate) { s.authGate = g }
func (s *Server) AuthGate() AuthGate     { return s.authGate }

// Authed reports whether the player passed the gate (always true without
// one); SetAuthed records it.
func (c *ClientConnection) Authed() bool      { return c.authed.Load() }
func (c *ClientConnection) SetAuthed(on bool) { c.authed.Store(on) }

// gateAuth is the per-action enforcement. Returns true if the action is
// allowed (no gate or player already authed); otherwise the player is told
// reason.
func gateAuth(c *ClientConnection, reason string) bool {
	if c.server == nil || c.server.authGate == nil || c.authed.Load() {
		return true
	}
	_ = c.sendSystemMessage(reason)
	return false
}

// IP is the player's address without the port (see ClientIP).
func (c *ClientConnection) IP() string { return ClientIP(c.link.RemoteAddr()) }

// ClientIP is the key the per-IP machinery (connection limits, auth IP
// bans, /banip, ConnectionsFromIP) knows a client by: for a *net.TCPAddr
// (the production case) its IPv4 address, or its IPv6 /64
// (connlimit.Key — one host owns a whole /64, so the full address would let
// it rotate past every per-IP limit); addr.String() for the net.Pipe
// connections used in tests.
func ClientIP(addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return connlimit.Key(tcp.IP)
	}
	if addr == nil {
		return ""
	}
	return addr.String()
}

// Done is closed once the connection is gone; Closed reports it.
func (c *ClientConnection) Done() <-chan struct{} { return c.link.Done() }
func (c *ClientConnection) Closed() bool          { return c.isClosed() }

// Disconnect kicks the player with reason and tears the connection down
// before returning (Kick leaves the teardown to a goroutine).
func (c *ClientConnection) Disconnect(reason string) {
	_ = c.sendPlayDisconnect(reason)
	c.cleanup()
}

// ResendCommands re-sends the command tree (Declare Commands) — the client
// highlights and tab-completes from it, and op-only commands are left out
// for non-operators, so it is stale after /op or /deop.
func (c *ClientConnection) ResendCommands() error { return c.sendDeclareCommands() }

// SetExperience sets the XP bar overlay: bar fill 0..1, the level number.
func (c *ClientConnection) SetExperience(bar float32, level, totalXP int32) error {
	return c.sendExperience(bar, level, totalXP)
}
