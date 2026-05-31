package cfg

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
	InitialOps = []string{"_LD5Coffee_", "LD5Coffee"}
)
