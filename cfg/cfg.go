package cfg

import (
	"sync/atomic"
	"time"
)

// Runtime-mutable configuration. Switched from const so OnlineMode can be
// flipped at startup (CLI flag, env var, etc.) without recompiling.
var (
	ServerId   = ""
	OnlineMode = false

	// MaxPlayers caps total online players across every instance. Sent in
	// the server-list ping (players.max) AND enforced at login: a name
	// landing on the (N+1)-th slot gets a "Server is full" disconnect.
	// Ops bypass the cap so admins can always rescue. Zero = unlimited.
	MaxPlayers = 20

	// InitialOps seeds the server's operator set on startup. These names
	// can immediately use /op, /gamemode, /tp without anyone /op-ing them.
	// Names are matched case-insensitively.
	InitialOps = []string{}

	// AuthEnabled controls whether the auth plugin (/register, /login,
	// auth instance, IP ban after failed attempts) is installed. Set
	// in main from the AUTH_ENABLED env var, with a sensible default:
	// on for offline mode, off for online mode (where Mojang already
	// vouches for identity). Read once at startup — no concurrent
	// mutation concern.
	AuthEnabled = false
)

// --- Auth plugin tunables (used by EnableAuth) ---
//
// Stored as atomic int64 nanoseconds / int32 so background goroutines
// (timeoutWatch in particular) can observe live changes without racing
// the test-suite cleanup that resets them between cases.

var (
	authTimeoutNanos atomic.Int64
	authMaxAttempts  atomic.Int32
	authBanNanos     atomic.Int64
)

func init() {
	authTimeoutNanos.Store(int64(30 * time.Second))
	authMaxAttempts.Store(3)
	authBanNanos.Store(int64(1 * time.Hour))
}

// AuthTimeout / SetAuthTimeout — grace period between login and the
// first successful /register or /login. Past this, the player is
// disconnected with "Auth timed out".
func AuthTimeout() time.Duration     { return time.Duration(authTimeoutNanos.Load()) }
func SetAuthTimeout(d time.Duration) { authTimeoutNanos.Store(int64(d)) }

// AuthMaxAttempts / SetAuthMaxAttempts — consecutive wrong-password
// attempts per IP before that IP gets auto-banned.
func AuthMaxAttempts() int     { return int(authMaxAttempts.Load()) }
func SetAuthMaxAttempts(n int) { authMaxAttempts.Store(int32(n)) }

// AuthBanDuration / SetAuthBanDuration — how long an IP stays banned
// once it crosses AuthMaxAttempts.
func AuthBanDuration() time.Duration     { return time.Duration(authBanNanos.Load()) }
func SetAuthBanDuration(d time.Duration) { authBanNanos.Store(int64(d)) }
