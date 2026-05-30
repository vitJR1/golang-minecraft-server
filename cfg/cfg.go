package cfg

// Runtime-mutable configuration. Switched from const so OnlineMode can be
// flipped at startup (CLI flag, env var, etc.) without recompiling.
var (
	ServerId   = ""
	OnlineMode = false

	// InitialOps seeds the server's operator set on startup. These names
	// can immediately use /op, /gamemode, /tp without anyone /op-ing them.
	// Names are matched case-insensitively.
	InitialOps = []string{"_LD5Coffee_", "LD5Coffee"}
)
