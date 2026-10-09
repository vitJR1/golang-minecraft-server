package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"minecraft-server/ban"
	"minecraft-server/bots"
	"minecraft-server/cfg"
	"minecraft-server/db"
	"minecraft-server/logger"
	"minecraft-server/redisc"
	"minecraft-server/schem"
	"minecraft-server/server"
	"minecraft-server/store"
	"minecraft-server/templates"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	// Each blank import registers a mini-game with game.Register during
	// its init(). Drop a game by deleting the line.
	_ "minecraft-server/games/bedwars"
	_ "minecraft-server/games/ffa"

	// Plugins: server-wide listeners + commands (game/plugin.go). Same
	// deal — one blank import each.
	_ "minecraft-server/plugins/worldedit"
)

const (
	templatesRoot   = templates.Root
	templateBaseY   = 64 // bottom of every imported schematic in world coords
	hubTemplateName = templates.Spawn
)

func main() {
	loadDotenv()  // .env → os.Setenv, BEFORE loadEnv reads them
	loadEnv()     // ONLINE_MODE / INITIAL_OPS → cfg.*
	logger.Init() // LOG_LEVEL / LOG_FORMAT read here

	srv := server.New()
	srv.ChatModerator = bots.NewNosleeperBot(srv)
	server.LoadFavicon(server.DefaultFaviconPath)
	connectStores(srv) // STORAGE=postgres|memory picks the backends
	// Auth plugin install gated by cfg.AuthEnabled (defaulted from
	// ONLINE_MODE in loadEnv, overridable via AUTH_ENABLED env).
	if cfg.AuthEnabled {
		server.EnableAuth(srv, "auth.json")
	}
	loadTemplates(srv)
	mountHubFromTemplate(srv, hubTemplateName)
	server.SetupLobbies(srv)
	server.SetupHubMenu(srv)

	// Ops endpoint (pprof + /stats JSON). Unauthenticated — keep it on
	// loopback and reach it via SSH; "off" disables entirely.
	if maddr := getEnv("METRICS_ADDR", "127.0.0.1:6060"); maddr != "off" {
		if _, err := server.StartMetrics(srv, maddr); err != nil {
			slog.Warn("metrics endpoint failed to start", "addr", maddr, "err", err)
		} else {
			slog.Info("metrics endpoint up", "addr", maddr)
		}
	}

	addr := ":" + getEnv("PORT", "25565")
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("listen failed", "addr", addr, "err", err)
		os.Exit(1)
	}
	slog.Info("listening", "addr", addr, "version", "1.20.1", "protocol", 763,
		"online_mode", cfg.OnlineMode)

	// Graceful shutdown: SIGINT/SIGTERM → stop accepting, kick everyone
	// with a message, stop tick loops, close backends. The accept loop
	// below waits on shutdownDone so the process exits only after the
	// disconnect packets have flushed.
	shutdownDone := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("shutting down", "signal", sig.String())
		_ = lis.Close() // unblocks Accept with net.ErrClosed
		srv.Shutdown("Server is restarting")
		if srv.Redis != nil {
			_ = srv.Redis.Close()
		}
		if srv.DB != nil {
			srv.DB.Close()
		}
		close(shutdownDone)
	}()

	for {
		conn, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				<-shutdownDone
				return
			}
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
		if d.IsDir() || !templates.IsSchem(path) {
			return nil
		}

		sch, err := schem.LoadFile(path)
		if err != nil {
			slog.Warn("template load failed", "path", path, "err", err)
			return nil // skip this one, continue walking
		}
		// Canonical forward-slash name, OS-independent (Windows filepath.Rel
		// would otherwise give backslash names that lookups can't match).
		name, ok := templates.Name(templatesRoot, path)
		if !ok {
			return nil
		}

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

// Storage modes selected by the STORAGE env var.
const (
	storagePostgres = "postgres" // default: Postgres for accounts/bans/stats
	storageMemory   = "memory"   // no database; nothing but bans persists
)

// defaultBanlistPath is the file bans go to under STORAGE=memory unless
// BANLIST_FILE overrides it ("off" / "none" keeps bans in RAM only).
const defaultBanlistPath = "banlist.json"

// storageMode reads STORAGE, defaulting to postgres. An unknown value is a
// config error, not something to guess around — abort.
func storageMode() string {
	switch v := strings.ToLower(getEnv("STORAGE", storagePostgres)); v {
	case storagePostgres, storageMemory:
		return v
	default:
		slog.Error("unknown STORAGE value", "value", v, "want", "postgres|memory")
		os.Exit(1)
		return ""
	}
}

// connectStores opens the storage backends for the selected STORAGE mode
// and stashes the handles on the server.
//
//   - postgres (default): Postgres is a HARD dependency — player accounts
//     and auth password hashes live there, so a server that boots without
//     it would let anyone /register over existing names and lose every
//     account created meanwhile. Connection or migration failure aborts
//     startup. Bans go to the bans table (store.BanStore).
//   - memory: no database. Accounts/passwords live in a process map, match
//     history and ratings aren't recorded. Bans use banlist.json
//     (ban.FileStore) so moderation survives restarts even in dev.
//
// Redis stays best-effort either way: a down Redis is logged and the server
// boots without it. It defaults on under postgres and off under memory;
// REDIS_ENABLED overrides both.
func connectStores(srv *server.Server) {
	ctx := context.Background()
	mode := storageMode()
	slog.Info("storage mode", "storage", mode)

	switch mode {
	case storagePostgres:
		connectPostgres(ctx, srv)
		srv.Bans = store.NewBanStore(srv.Store.Players, srv.Store.Bans)
		importBanlist(ctx, srv.Bans)
	case storageMemory:
		slog.Warn("STORAGE=memory: accounts, passwords, match history and ratings are NOT persisted")
		srv.Bans = memoryBanStore()
	}

	if envEnabled("REDIS_ENABLED", mode == storagePostgres) {
		rc, err := redisc.Connect(ctx, redisc.ConfigFromEnv())
		if err != nil {
			slog.Warn("redis connect failed; continuing without it", "err", err)
		} else {
			srv.Redis = rc
			slog.Info("redis connected")
		}
	}
}

// connectPostgres connects, migrates and installs the repositories, or exits.
func connectPostgres(ctx context.Context, srv *server.Server) {
	dbCfg := db.ConfigFromEnv()
	pg, err := db.Connect(ctx, dbCfg)
	if err != nil {
		slog.Error("postgres connect failed — Postgres is required, refusing to start",
			"err", err, "hint", "docker compose up -d")
		os.Exit(1)
	}
	srv.DB = pg
	// Bring the schema up to date, then expose the repositories.
	if err := store.Migrate(dbCfg.DSN()); err != nil {
		slog.Error("db migrations failed — refusing to start on a stale schema", "err", err)
		os.Exit(1)
	}
	srv.Store = store.New(pg.Pool)
	slog.Info("postgres connected, migrations applied")
}

// memoryBanStore builds the STORAGE=memory ban backend: banlist.json (or
// BANLIST_FILE) when a path is configured, a pure in-memory map otherwise.
// A corrupt file is logged and falls back to memory rather than aborting —
// bans aren't worth refusing to boot over.
func memoryBanStore() ban.Store {
	path := getEnv("BANLIST_FILE", defaultBanlistPath)
	switch strings.ToLower(path) {
	case "off", "none":
		slog.Info("bans: in-memory only (BANLIST_FILE=" + path + ")")
		return ban.NewMemoryStore()
	}
	fs, err := ban.NewFileStore(path)
	if err != nil {
		slog.Warn("bans: failed to load banlist, using in-memory store", "path", path, "err", err)
		return ban.NewMemoryStore()
	}
	slog.Info("bans: file-backed", "path", path)
	return fs
}

// importBanlist is the one-shot memory→postgres migration for bans: under
// STORAGE=postgres a leftover banlist.json (or BANLIST_FILE) is copied into
// the bans table, then renamed to <path>.imported so the next boot doesn't
// repeat it. Entries the database already bans are kept as-is (ban.Import).
// A failure leaves the file in place and logs — bans aren't worth refusing
// to boot over, and the next start retries.
func importBanlist(ctx context.Context, dst ban.Store) {
	path := getEnv("BANLIST_FILE", defaultBanlistPath)
	switch strings.ToLower(path) {
	case "off", "none":
		return
	}
	entries, err := ban.ReadFile(path)
	if err != nil {
		slog.Warn("bans: banlist import skipped, file unreadable", "path", path, "err", err)
		return
	}
	if entries == nil { // no file — nothing to migrate
		return
	}
	res, err := ban.Import(ctx, dst, entries)
	if err != nil {
		slog.Warn("bans: banlist import failed, file left in place for retry",
			"path", path, "added", res.Added, "err", err)
		return
	}
	done := path + ".imported"
	if err := os.Rename(path, done); err != nil {
		slog.Warn("bans: imported banlist but could not rename it; it will be re-scanned next boot",
			"path", path, "err", err)
	}
	slog.Info("bans: banlist imported into Postgres", "path", path,
		"added", res.Added, "kept_existing", res.Kept, "expired", res.Expired, "renamed_to", done)
}

// envEnabled reads a boolean-ish env var, returning def when unset or
// unrecognized. Mirrors the truthy/falsy words accepted elsewhere.
func envEnabled(name string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return def
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
	// Connection-flood limits. Unlike MAX_PLAYERS, zero is meaningful here
	// (= disable that limit), so >= 0 is accepted.
	if v := os.Getenv("MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			cfg.MaxConns = n
		}
	}
	if v := os.Getenv("MAX_CONNS_PER_IP"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			cfg.MaxConnsPerIP = n
		}
	}
	if v := os.Getenv("CONN_RATE_PER_IP"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			cfg.ConnRatePerIP = n
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
	// Combat model: PVP_VERSION=1 → 1.8, 2 → 1.9. Anything else is ignored
	// (keeps the default). Only 1/2 are valid so a typo can't silently pick
	// a bogus mode.
	switch strings.TrimSpace(os.Getenv("PVP_VERSION")) {
	case "1":
		cfg.PvPVersion = 1
	case "2":
		cfg.PvPVersion = 2
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
