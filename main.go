package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"minecraft-server/ban"
	"minecraft-server/bots"
	"minecraft-server/cfg"
	"minecraft-server/logger"
	"minecraft-server/schem"
	"minecraft-server/server"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	// Each blank import registers a mini-game with game.Register during
	// its init(). Drop a game by deleting the line.
	_ "minecraft-server/games/ffa"
)

const (
	templatesRoot   = "schem/templates"
	templateBaseY   = 64 // bottom of every imported schematic in world coords
	hubTemplateName = "spawn"
)

func main() {
	loadDotenv()  // .env → os.Setenv, BEFORE loadEnv reads them
	loadEnv()     // ONLINE_MODE / INITIAL_OPS → cfg.*
	logger.Init() // LOG_LEVEL / LOG_FORMAT read here

	if err := ban.Load("banlist.json"); err != nil {
		slog.Warn("failed to load banlist", "path", "banlist.json", "err", err)
	}

	srv := server.New()
	srv.ChatModerator = bots.NewNosleeperBot(srv)
	server.LoadFavicon(server.DefaultFaviconPath)
	// Auth plugin install gated by cfg.AuthEnabled (defaulted from
	// ONLINE_MODE in loadEnv, overridable via AUTH_ENABLED env).
	if cfg.AuthEnabled {
		server.EnableAuth(srv, "auth.json")
	}
	loadTemplates(srv)
	mountHubFromTemplate(srv, hubTemplateName)
	server.SetupLobbies(srv)
	server.SetupHubMenu(srv)

	addr := ":" + getEnv("PORT", "25565")
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("listen failed", "addr", addr, "err", err)
		os.Exit(1)
	}
	slog.Info("listening", "addr", addr, "version", "1.20.1", "protocol", 763,
		"online_mode", cfg.OnlineMode)

	for {
		conn, err := lis.Accept()
		if err != nil {
			slog.Error("accept failed", "err", err)
			continue
		}
		go srv.HandleConn(conn)
	}
}

// loadTemplates walks templatesRoot recursively and registers every
// *.schem file as a world.Template under its relative path (sans
// extension). After this, /instance create <id> <name> can clone any
// of them, and /template list shows the same set the disk has.
//
// Each schematic is centred horizontally around (0,_,0) — its corner
// lands at (-width/2, templateBaseY, -length/2) — so the default spawn
// at (0.5, 67, 0.5) drops the player on top instead of beside.
//
// A missing root is fine (server boots empty); per-file load errors are
// logged but not fatal.
func loadTemplates(srv *server.Server) {
	err := filepath.WalkDir(templatesRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == templatesRoot {
				return filepath.SkipAll // no templates dir → nothing to load
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".schem") {
			return nil
		}

		sch, err := schem.LoadFile(path)
		if err != nil {
			slog.Warn("template load failed", "path", path, "err", err)
			return nil // skip this one, continue walking
		}
		rel, _ := filepath.Rel(templatesRoot, path)
		name := strings.TrimSuffix(rel, filepath.Ext(rel))

		originX := -int(sch.Width) / 2
		originZ := -int(sch.Length) / 2
		tmpl := sch.ToTemplateAt(originX, templateBaseY, originZ)
		srv.RegisterTemplate(name, tmpl)
		slog.Info("template loaded",
			"name", name, "path", path,
			"size", fmt.Sprintf("%dx%dx%d", sch.Width, sch.Height, sch.Length),
			"blocks", len(sch.Blocks))
		return nil
	})
	if err != nil {
		slog.Warn("template scan failed", "root", templatesRoot, "err", err)
	}
}

// loadDotenv reads a `.env` file from the working directory (if present)
// and exports each KEY=VALUE into the process environment, where the
// rest of the bootstrap (loadEnv, logger.Init, getEnv) will pick them
// up via os.Getenv. Already-set env vars take precedence — godotenv
// won't overwrite, so `LOG_LEVEL=debug ./mc` still wins over `.env`.
//
// Missing file is silent (development setups often omit .env). Parse
// errors warn but don't crash — bad lines just don't populate.
func loadDotenv() {
	if err := godotenv.Load(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		// stderr fallback: logger isn't initialized yet at this point.
		_, _ = fmt.Fprintln(os.Stderr, "dotenv: load failed:", err)
	}
}

// loadEnv reads optional environment variables and updates the shared
// cfg package + leaves PORT / LOG_* for their direct consumers (the
// listener in main, logger.Init). Stays here (not in cfg) so the
// env-var → config mapping is in one obvious place.
//
// Vars: see .env.example for the full list with defaults.
func loadEnv() {
	switch strings.ToLower(os.Getenv("ONLINE_MODE")) {
	case "true", "1", "yes", "on":
		cfg.OnlineMode = true
	case "false", "0", "no", "off":
		cfg.OnlineMode = false
	}

	// Auth plugin default: on for offline mode, off for online mode.
	// AUTH_ENABLED env var explicitly overrides if set — useful for
	// flipping auth off in offline-mode for testing, or on in online-
	// mode for defense in depth.
	cfg.AuthEnabled = !cfg.OnlineMode
	switch strings.ToLower(os.Getenv("AUTH_ENABLED")) {
	case "true", "1", "yes", "on":
		cfg.AuthEnabled = true
	case "false", "0", "no", "off":
		cfg.AuthEnabled = false
	}
	if v := os.Getenv("MAX_PLAYERS"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cfg.MaxPlayers = n
		}
	}
	// Auth knobs. time.ParseDuration handles "30s", "5m", "1h30m", etc.
	if v := os.Getenv("AUTH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil && d > 0 {
			cfg.SetAuthTimeout(d)
		}
	}
	if v := os.Getenv("AUTH_MAX_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cfg.SetAuthMaxAttempts(n)
		}
	}
	if v := os.Getenv("AUTH_BAN_DURATION"); v != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil && d > 0 {
			cfg.SetAuthBanDuration(d)
		}
	}
	if ops := os.Getenv("INITIAL_OPS"); ops != "" {
		var parsed []string
		for _, n := range strings.Split(ops, ",") {
			if n = strings.TrimSpace(n); n != "" {
				parsed = append(parsed, n)
			}
		}
		if len(parsed) > 0 {
			cfg.InitialOps = parsed
		}
	}
}

// getEnv returns the value of name or fallback if unset/empty. Trims
// surrounding whitespace so "PORT= 25565 " works.
func getEnv(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// mountHubFromTemplate clones the named template into Hub.World. Runs
// before the listener so direct mutation of Hub.World is race-free.
// Missing template → hub stays with its empty MemoryWorld (warns).
func mountHubFromTemplate(srv *server.Server, name string) {
	tmpl := srv.GetTemplate(name)
	if tmpl == nil {
		slog.Warn("hub template not found", "name", name,
			"hint", "place "+name+".schem under "+templatesRoot+"/")
		return
	}
	srv.Hub.World = tmpl.Instantiate()
	slog.Info("hub mounted from template", "name", name)
}
