package server

import (
	"net"
	"time"
)

// auth_gate.go: the hook the offline-mode auth gate (server/authgate) plugs
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

// ClientIP extracts the IP portion of a net.Addr. Works for *net.TCPAddr
// (the production case) and falls back to addr.String() for net.Pipe
// connections used in tests.
func ClientIP(addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.String()
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

// SetExperience sets the XP bar overlay: bar fill 0..1, the level number.
func (c *ClientConnection) SetExperience(bar float32, level, totalXP int32) error {
	return c.sendExperience(bar, level, totalXP)
}
