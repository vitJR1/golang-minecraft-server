package cfg

// Runtime-mutable configuration. Switched from const so OnlineMode can be
// flipped at startup (CLI flag, env var, etc.) without recompiling.
var (
	ServerId   = ""
	OnlineMode = false
)
