package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"minecraft-server/player"
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
	mode, ok := parseGamemode(args[0])
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

func parseGamemode(s string) (player.Gamemode, bool) {
	switch strings.ToLower(s) {
	case "survival", "s", "0":
		return player.Survival, true
	case "creative", "c", "1":
		return player.Creative, true
	case "adventure", "a", "2":
		return player.Adventure, true
	case "spectator", "sp", "3":
		return player.Spectator, true
	}
	return 0, false
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
		x, err := strconv.ParseFloat(args[0], 64)
		if err != nil {
			_ = c.sendSystemMessage("Bad X: " + args[0])
			return
		}
		y, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			_ = c.sendSystemMessage("Bad Y: " + args[1])
			return
		}
		z, err := strconv.ParseFloat(args[2], 64)
		if err != nil {
			_ = c.sendSystemMessage("Bad Z: " + args[2])
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
		_ = c.sendSystemMessage("Usage: /effect <player> <" + strings.Join(effectNames(), "|") + "> [level] [seconds] | /effect <player> clear")
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
	effect, ok := knownEffects[strings.ToLower(args[1])]
	if !ok {
		_ = c.sendSystemMessage("Unknown effect: " + args[1] + " (" + strings.Join(effectNames(), ", ") + ")")
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
	c.server.effects.apply(target, effect, level, time.Duration(seconds)*time.Second)
	_ = c.sendSystemMessage(fmt.Sprintf("Gave %s %s %d for %ds", target.playerName, effect.Name, level, seconds))
}
