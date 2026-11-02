package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"minecraft-server/server/command"
	"minecraft-server/server/effect"
)

// cmd_admin.go: operator commands on players — /op /deop /gamemode /tp
// /effect.

func init() {
	registerCommand(&Command{
		Name:     "op",
		NeedsOp:  true,
		Help:     "/op <player> — grant operator privileges",
		Run:      cmdOp,
		Complete: playerArg(1),
	})
	registerCommand(&Command{
		Name:     "deop",
		NeedsOp:  true,
		Help:     "/deop <player> — revoke operator privileges",
		Run:      cmdDeop,
		Complete: playerArg(1),
	})
	registerCommand(&Command{
		Name:     "gamemode",
		Aliases:  []string{"gm"},
		NeedsOp:  true,
		Help:     "/gamemode <survival|creative|adventure|spectator> [player]",
		Run:      cmdGamemode,
		Complete: playerArg(2),
	})
	registerCommand(&Command{
		Name:     "effect",
		NeedsOp:  true,
		Help:     "/effect <player> <effect> [level] [seconds] | /effect <player> clear (speed, slowness, haste, mining_fatigue, jump_boost, regeneration, invisibility, blindness)",
		Run:      cmdEffect,
		Complete: completeEffect,
	})
	registerCommand(&Command{
		Name:     "tp",
		Aliases:  []string{"teleport"},
		NeedsOp:  true,
		Help:     "/tp <player> | /tp <x> <y> <z>",
		Run:      cmdTp,
		Complete: playerArg(1),
	})
}

func cmdOp(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /op <player>")
		return
	}
	target := args[0]
	c.server.Ops.Add(target)
	_ = c.sendSystemMessage("Granted op to " + target)
	if conn, _, ok := c.server.FindPlayer(target); ok {
		_ = conn.sendSystemMessage("You are now an operator")
	}
}

func cmdDeop(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /deop <player>")
		return
	}
	target := args[0]
	c.server.Ops.Remove(target)
	_ = c.sendSystemMessage("Revoked op from " + target)
	if conn, _, ok := c.server.FindPlayer(target); ok {
		_ = conn.sendSystemMessage("You are no longer an operator")
	}
}

// --- /gamemode ---

func cmdGamemode(c *ClientConnection, args []string) {
	if len(args) < 1 || len(args) > 2 {
		_ = c.sendSystemMessage("Usage: /gamemode <survival|creative|adventure|spectator> [player]")
		return
	}
	mode, ok := command.ParseGamemode(args[0])
	if !ok {
		_ = c.sendSystemMessage("Unknown gamemode: " + args[0])
		return
	}
	target := c
	if len(args) == 2 {
		conn, _, ok := c.server.FindPlayer(args[1])
		if !ok {
			_ = c.sendSystemMessage("Player not found: " + args[1])
			return
		}
		target = conn
	}
	target.player.SetGamemode(mode)
	_ = target.sendGameModeChange(mode)
	_ = c.sendSystemMessage(fmt.Sprintf("Set %s's gamemode to %s", target.player.Name, args[0]))
	if target != c {
		_ = target.sendSystemMessage("Your gamemode is now " + args[0])
	}
}

// --- /tp ---

func cmdTp(c *ClientConnection, args []string) {
	switch len(args) {
	case 1:
		// /tp <player> — teleport sender to target. Only works inside the
		// same instance for now — cross-instance teleport needs more wiring.
		target, inst, ok := c.server.FindPlayer(args[0])
		if !ok {
			_ = c.sendSystemMessage("Player not found: " + args[0])
			return
		}
		if inst != c.instance {
			_ = c.sendSystemMessage("Player is in another instance")
			return
		}
		s := target.player.Snapshot()
		teleportConnTo(c, s.X, s.Y, s.Z)
		_ = c.sendSystemMessage("Teleported to " + target.player.Name)
	case 3:
		// /tp <x> <y> <z>
		x, y, z, err := command.ParseCoords(args)
		if err != nil {
			_ = c.sendSystemMessage(err.Error())
			return
		}
		teleportConnTo(c, x, y, z)
		_ = c.sendSystemMessage(fmt.Sprintf("Teleported to %.2f, %.2f, %.2f", x, y, z))
	default:
		_ = c.sendSystemMessage("Usage: /tp <player>  or  /tp <x> <y> <z>")
	}
}

// teleportConnTo updates the player's authoritative position and tells the
// client to repaint at the new coords. Other players see the new position
// via the standard entity-teleport broadcast on the next tick.
func teleportConnTo(c *ClientConnection, x, y, z float64) {
	c.player.MoveTo(x, y, z, false)
	_ = c.sendSyncPlayerPosition(x, y, z)
	c.broadcastEntityTeleport()
}

// --- /effect ---

// cmdEffect grants or clears a potion effect on a player (modifiers.go).
//
//	/effect <player> <effect> [level=1] [seconds=30]
//	/effect <player> clear
func cmdEffect(c *ClientConnection, args []string) {
	if len(args) < 2 {
		_ = c.sendSystemMessage("Usage: /effect <player> <" + strings.Join(effect.Names(), "|") + "> [level] [seconds] | /effect <player> clear")
		return
	}
	target, _, ok := c.server.FindPlayer(args[0])
	if !ok {
		_ = c.sendSystemMessage("Player not online: " + args[0])
		return
	}
	if strings.EqualFold(args[1], "clear") {
		c.server.effects.remove(target, nil)
		_ = c.sendSystemMessage("Cleared effects on " + target.playerName)
		return
	}
	eff, ok := effect.Known[strings.ToLower(args[1])]
	if !ok {
		_ = c.sendSystemMessage("Unknown effect: " + args[1] + " (" + strings.Join(effect.Names(), ", ") + ")")
		return
	}
	level, seconds := 1, 30
	if len(args) >= 3 {
		if v, err := strconv.Atoi(args[2]); err == nil && v >= 1 && v <= 255 {
			level = v
		} else {
			_ = c.sendSystemMessage("Level must be 1..255")
			return
		}
	}
	if len(args) >= 4 {
		if v, err := strconv.Atoi(args[3]); err == nil && v >= 1 {
			seconds = v
		} else {
			_ = c.sendSystemMessage("Seconds must be ≥1")
			return
		}
	}
	c.server.effects.apply(target, eff, level, time.Duration(seconds)*time.Second)
	_ = c.sendSystemMessage(fmt.Sprintf("Gave %s %s %d for %ds", target.playerName, eff.Name, level, seconds))
}
