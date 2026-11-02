package server

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"minecraft-server/game"
	"minecraft-server/server/lobby"
)

// cmd_game.go: getting into and out of games — /play /hub /leave
// /forcestart.

func init() {
	registerCommand(&Command{
		Name:     "play",
		Aliases:  []string{"queue", "q"},
		NeedsOp:  false,
		Help:     "/play <game> [arena] | /play leave | /play list — matchmaker",
		Run:      cmdPlay,
		Complete: completePlay,
	})
	registerCommand(&Command{
		Name:    "hub",
		Aliases: []string{"lobby"},
		NeedsOp: false,
		Help:    "/hub — teleport back to the hub instance",
		Run:     cmdHub,
	})
	registerCommand(&Command{
		Name: "leave",
		Help: "/leave — leave the game for its mode's lobby (BedWars, FFA, …)",
		Run:  cmdLeave,
	})
	registerCommand(&Command{
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
func cmdPlay(c *ClientConnection, args []string) {
	if len(args) == 0 {
		_ = c.sendSystemMessage("Usage: /play <game> | /play leave | /play list | /play status")
		return
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "leave", "cancel":
		c.server.Matchmaker.Dequeue(c)
		_ = c.sendSystemMessage("Left matchmaker queue")
	case "list", "ls":
		// Base games only; created arenas are listed via /arena list.
		var defs []*game.Definition
		for _, d := range game.All() {
			if !c.server.IsArena(d.ID) {
				defs = append(defs, d)
			}
		}
		if len(defs) == 0 {
			_ = c.sendSystemMessage("No games registered.")
			return
		}
		sort.Slice(defs, func(i, j int) bool { return defs[i].ID < defs[j].ID })
		_ = c.sendSystemMessage(fmt.Sprintf("Registered games (%d):", len(defs)))
		for _, d := range defs {
			_ = c.sendSystemMessage(fmt.Sprintf("  %s (%s) — %d/%d waiting, needs %d to start",
				d.ID, d.Name, c.server.Matchmaker.QueueSize(d.ID), d.MaxPlayers, d.MinPlayers))
		}
	case "status":
		if gameID, ok := c.server.Matchmaker.PlayerQueue(c); ok {
			_ = c.sendSystemMessage("Queued for " + gameID +
				" (" + strconv.Itoa(c.server.Matchmaker.QueueSize(gameID)) + " waiting)")
		} else {
			_ = c.sendSystemMessage("Not in any matchmaker queue")
		}
	default:
		// /play <game> <arena>: join that arena's running instance directly
		// (created by /arena create), so all players share one world and see
		// each other. /play <game> with no arena uses the matchmaker.
		if len(args) >= 2 {
			arena := args[1]
			if !c.server.IsArena(arena) {
				_ = c.sendSystemMessage("Unknown arena: " + arena + " (see /arena list " + sub + ")")
				return
			}
			target := c.server.GetInstance(arena)
			if target == nil {
				_ = c.sendSystemMessage("Arena " + arena + " is not running — recreate it with /arena create.")
				return
			}
			if target == c.instance {
				_ = c.sendSystemMessage("Already in " + arena)
				return
			}
			if err := c.server.MovePlayer(c, target, 0, 80, 0); err != nil {
				_ = c.sendSystemMessage("Couldn't join " + arena + ": " + err.Error())
				return
			}
			_ = c.sendSystemMessage("Joined arena " + arena)
			return
		}
		if err := c.server.Matchmaker.Queue(c, sub); err != nil {
			_ = c.sendSystemMessage("Queue failed: " + err.Error())
			return
		}
		_ = c.sendSystemMessage("Queued for " + sub +
			" (" + strconv.Itoa(c.server.Matchmaker.QueueSize(sub)) + " waiting)")
	}
}

// cmdHub is the only fixed-destination teleport command — every player
// can run it (no NeedsOp) since games need an "escape" back to the
// lobby. From hub itself it's a polite no-op.
//
// Default spawn (0.5, 67, 0.5) mirrors player.New's defaults so the
// player lands on top of the spawn schematic in the centre column.
func cmdHub(c *ClientConnection, args []string) {
	_ = args
	// Always drop any matchmaker queue — explicitly choosing "/hub" is
	// the user saying "I don't want a game right now", whether they're
	// in the hub already or coming back from an arena.
	c.server.Matchmaker.Dequeue(c)

	if c.instance == c.server.Hub {
		_ = c.sendSystemMessage("You're already in the hub.")
		return
	}
	if err := c.server.MovePlayer(c, c.server.Hub, 0.5, 67, 0.5); err != nil {
		_ = c.sendSystemMessage("Hub teleport failed: " + err.Error())
		return
	}
}

// cmdLeave: /leave — out of the running game into the lobby of its mode, the
// way the hub menu enters a lobby (spectator mode, kit and effects are shed
// on landing). A game without a lobby, or none at all, falls back to the
// hub.
func cmdLeave(c *ClientConnection, _ []string) {
	c.server.Matchmaker.Dequeue(c)
	inst := c.instance
	if inst == c.server.Hub {
		_ = c.sendSystemMessage("You're not in a game.")
		return
	}
	if lobby.Is(inst.ID) {
		_ = c.sendSystemMessage("You're not in a game — /hub to go back to the hub.")
		return
	}
	target := c.server.Hub
	if id, ok := lobby.ForGame(inst.GameID); ok {
		if dest := c.server.GetInstance(id); dest != nil {
			target = dest
		}
	}
	if err := c.server.MovePlayer(c, target, 0.5, 67, 0.5); err != nil {
		_ = c.sendSystemMessage("Couldn't leave: " + err.Error())
	}
}

// cmdForceStart: /forcestart — start the round of the game the operator is
// in now (game.ForceStarter), however few players are waiting.
func cmdForceStart(c *ClientConnection, _ []string) {
	start := c.instance.OnForceStart
	if start == nil {
		_ = c.sendSystemMessage("This isn't a game that can be force-started.")
		return
	}
	if err := start(); err != nil {
		_ = c.sendSystemMessage("Can't force-start: " + err.Error())
		return
	}
	_ = c.sendSystemMessage("Starting the game now.")
}
