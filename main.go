package main

import (
	"log/slog"
	"minecraft-server/ban"
	"minecraft-server/bots"
	"minecraft-server/logger"
	"minecraft-server/schem"
	"minecraft-server/server"
	"net"
	"os"

	// Each blank import registers a mini-game with game.Register during
	// its init(). Drop a game by deleting the line.
	_ "minecraft-server/games/ffa"
)

func main() {
	logger.Init()

	if err := ban.Load("banlist.json"); err != nil {
		slog.Warn("failed to load banlist", "path", "banlist.json", "err", err)
	}

	srv := server.New()
	srv.ChatModerator = bots.NewNosleeperBot(srv)
	loadHubSpawn(srv)
	server.SetupHubMenu(srv)

	const addr = ":25565"
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("listen failed", "addr", addr, "err", err)
		os.Exit(1)
	}
	slog.Info("listening", "addr", addr, "version", "1.20.1", "protocol", 763)

	for {
		conn, err := lis.Accept()
		if err != nil {
			slog.Error("accept failed", "err", err)
			continue
		}
		go srv.HandleConn(conn)
	}
}

// loadHubSpawn drops the schematic at schem/templates/spawn.schem onto
// the hub's world. Runs before the listener accepts any connection, so
// directly mutating Hub.World is safe (no concurrent readers).
//
// The schematic is centred horizontally around (0, _, 0) — its corner
// lands at (-width/2, baseY, -length/2) — so players spawning at the
// default (0, 80, 0) fall down onto/into the build instead of beside it.
func loadHubSpawn(srv *server.Server) {
	const (
		path  = "schem/templates/spawn.schem"
		baseY = 64 // bottom of the schematic in world coords
	)
	sch, err := schem.LoadFile(path)
	if err != nil {
		slog.Warn("skipping spawn load", "path", path, "err", err)
		return
	}
	originX := -int(sch.Width) / 2
	originZ := -int(sch.Length) / 2
	tmpl := sch.ToTemplateAt(originX, baseY, originZ)
	srv.Hub.World = tmpl.Instantiate()
	slog.Info("loaded spawn schematic",
		"path", path,
		"width", sch.Width, "height", sch.Height, "length", sch.Length,
		"blocks", len(sch.Blocks),
		"origin_x", originX, "origin_y", baseY, "origin_z", originZ)
}
