package commands

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"minecraft-server/game"
	"minecraft-server/server"
)

// cmd_game.go: getting into and out of games — /play /hub /leave
// /forcestart.

func init() {
	server.RegisterCommand(&server.Command{
		Name:     "play",
		Aliases:  []string{"queue", "q"},
		NeedsOp:  false,
		Help:     "/play <game> [arena] | /play leave | /play list — matchmaker",
		Run:      cmdPlay,
		Complete: completePlay,
	})
	server.RegisterCommand(&server.Command{
		Name:    "hub",
		Aliases: []string{"lobby"},
		NeedsOp: false,
		Help:    "/hub — teleport back to the hub instance",
		Run:     cmdHub,
	})
	server.RegisterCommand(&server.Command{
		Name: "leave",
		Help: "/leave — leave the game for its mode's lobby (BedWars, FFA, …)",
		Run:  cmdLeave,
	})
	server.RegisterCommand(&server.Command{
		Name:    "forcestart",
		NeedsOp: true,
		Help:    "/forcestart — start this game's round now, without waiting for players",
		Run:     cmdForceStart,
	})
}

// cmdPlay routes the matchmaker subcommands.
//
//	/play <gameID>   — join the queue for that game
//	/play leave      — leave whichever queue you're in
//	/play list       — show registered games + queue sizes
//	/play status     — show what queue you're currently in (if any)
func cmdPlay(c *server.ClientConnection, args []string) {
	if len(args) == 0 {
		_ = c.SendMessage("Usage: /play <game> | /play leave | /play list | /play status")
		return
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "leave", "cancel":
		c.Server().Matchmaker.Dequeue(c)
		_ = c.SendMessage("Left matchmaker queue")
	case "list", "ls":
		// Base games only; created arenas are listed via /arena list.
		var defs []*game.Definition
		for _, d := range game.All() {
			if !c.Server().IsArena(d.ID) {
				defs = append(defs, d)
			}
		}
		if len(defs) == 0 {
			_ = c.SendMessage("No games registered.")
			return
		}
		sort.Slice(defs, func(i, j int) bool { return defs[i].ID < defs[j].ID })
		_ = c.SendMessage(fmt.Sprintf("Registered games (%d):", len(defs)))
		for _, d := range defs {
			_ = c.SendMessage(fmt.Sprintf("  %s (%s) — %d/%d waiting, needs %d to start",
				d.ID, d.Name, c.Server().Matchmaker.QueueSize(d.ID), d.MaxPlayers, d.MinPlayers))
		}
	case "status":
		if gameID, ok := c.Server().Matchmaker.PlayerQueue(c); ok {
			_ = c.SendMessage("Queued for " + gameID +
				" (" + strconv.Itoa(c.Server().Matchmaker.QueueSize(gameID)) + " waiting)")
		} else {
			_ = c.SendMessage("Not in any matchmaker queue")
		}
	default:
		// /play <game> <arena>: join that arena's running instance directly
		// (created by /arena create), so all players share one world and see
		// each other. /play <game> with no arena uses the matchmaker.
		if len(args) >= 2 {
			arena := args[1]
			if !c.Server().IsArena(arena) {
				_ = c.SendMessage("Unknown arena: " + arena + " (see /arena list " + sub + ")")
				return
			}
			target := c.Server().GetInstance(arena)
			if target == nil {
				_ = c.SendMessage("Arena " + arena + " is not running — recreate it with /arena create.")
				return
			}
			if target == c.Instance() {
				_ = c.SendMessage("Already in " + arena)
				return
			}
			if err := c.Server().Join(c, target); err != nil {
				_ = c.SendMessage("Couldn't join " + arena + ": " + err.Error())
				return
			}
			_ = c.SendMessage("Joined arena " + arena)
			return
		}
		if err := c.Server().Matchmaker.Queue(c, sub); err != nil {
			_ = c.SendMessage("Queue failed: " + err.Error())
			return
		}
		_ = c.SendMessage("Queued for " + sub +
			" (" + strconv.Itoa(c.Server().Matchmaker.QueueSize(sub)) + " waiting)")
	}
}

// cmdHub is the only fixed-destination teleport command — every player
// can run it (no NeedsOp) since games need an "escape" back to the
// lobby. From hub itself it's a polite no-op.
//
// The hub's Arrival (0.5, 67, 0.5) mirrors player.New's defaults so the
// player lands on top of the spawn schematic in the centre column.
func cmdHub(c *server.ClientConnection, _ []string) {
	moved, err := c.Server().ToHub(c)
	switch {
	case err != nil:
		_ = c.SendMessage("Hub teleport failed: " + err.Error())
	case !moved:
		_ = c.SendMessage("You're already in the hub.")
	}
}

// cmdLeave: /leave — out of the running game into the lobby of its mode
// (server.LeaveGame); a game without a lobby falls back to the hub.
func cmdLeave(c *server.ClientConnection, _ []string) {
	reason, err := c.Server().LeaveGame(c)
	switch {
	case reason != "":
		_ = c.SendMessage(reason)
	case err != nil:
		_ = c.SendMessage("Couldn't leave: " + err.Error())
	}
}

// cmdForceStart: /forcestart — start the round of the game the operator is
// in now (game.ForceStarter), however few players are waiting.
func cmdForceStart(c *server.ClientConnection, _ []string) {
	start := c.Instance().OnForceStart
	if start == nil {
		_ = c.SendMessage("This isn't a game that can be force-started.")
		return
	}
	if err := start(); err != nil {
		_ = c.SendMessage("Can't force-start: " + err.Error())
		return
	}
	_ = c.SendMessage("Starting the game now.")
}
