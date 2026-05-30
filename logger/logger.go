// Package logger configures the process-wide slog.Default handler.
//
// One Init call in main wires every package's slog usage to the same
// stream and level. Everywhere else in the codebase imports stdlib
// "log/slog" directly and calls slog.Info / Debug / Warn / Error.
//
// Format defaults to a human-readable text handler. Set LOG_FORMAT=json
// for line-delimited JSON suitable for log aggregators. Level is
// controlled by LOG_LEVEL ∈ {debug, info, warn, error}, default info.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// Init sets slog.Default based on LOG_LEVEL and LOG_FORMAT env vars.
// Safe to call more than once; the last call wins.
func Init() {
	level := parseLevel(os.Getenv("LOG_LEVEL"))
	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	switch strings.ToLower(os.Getenv("LOG_FORMAT")) {
	case "json":
		h = slog.NewJSONHandler(os.Stdout, opts)
	default:
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
