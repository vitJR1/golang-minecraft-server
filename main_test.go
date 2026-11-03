package main

import (
	"testing"
	"time"

	"minecraft-server/cfg"
)

// TestEnvHelpers: values are trimmed (" true" from a .env used to be
// ignored for ONLINE_MODE / AUTH_ENABLED) and unusable ones fall back to the
// default.
func TestEnvHelpers(t *testing.T) {
	t.Setenv("T_BOOL", " true ")
	t.Setenv("T_BOOL_BAD", "ture")
	if !envEnabled("T_BOOL", false) || !envEnabled("T_BOOL_BAD", true) || envEnabled("T_BOOL_UNSET", false) {
		t.Error("envEnabled")
	}
	t.Setenv("T_INT", " 42 ")
	t.Setenv("T_INT_BAD", "4x")
	t.Setenv("T_INT_LOW", "0")
	if envInt("T_INT", 1, 1) != 42 || envInt("T_INT_BAD", 7, 0) != 7 || envInt("T_INT_LOW", 7, 1) != 7 || envInt("T_INT_UNSET", 7, 0) != 7 {
		t.Error("envInt")
	}
	t.Setenv("T_DUR", " 90s")
	t.Setenv("T_DUR_BAD", "90")
	t.Setenv("T_DUR_NEG", "-1s")
	if envDuration("T_DUR", time.Second) != 90*time.Second || envDuration("T_DUR_BAD", time.Second) != time.Second ||
		envDuration("T_DUR_NEG", time.Second) != time.Second {
		t.Error("envDuration")
	}
}

// TestLoadEnvOnlineModeTrimmed: ONLINE_MODE with stray whitespace turns
// online mode on and, through it, the auth plugin's default off.
func TestLoadEnvOnlineModeTrimmed(t *testing.T) {
	origMode, origAuth := cfg.OnlineMode, cfg.AuthEnabled
	t.Cleanup(func() { cfg.OnlineMode, cfg.AuthEnabled = origMode, origAuth })
	t.Setenv("ONLINE_MODE", "true ")
	t.Setenv("AUTH_ENABLED", "")
	loadEnv()
	if !cfg.OnlineMode || cfg.AuthEnabled {
		t.Errorf("online=%v auth=%v", cfg.OnlineMode, cfg.AuthEnabled)
	}
}
