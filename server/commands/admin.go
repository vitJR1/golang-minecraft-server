package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"minecraft-server/server"
	"minecraft-server/server/command"
	"minecraft-server/server/effect"
)

// cmd_admin.go: operator commands on players — /op /deop /gamemode /tp
// /effect.

func init() {
	server.RegisterCommand(&server.Command{
		Name:     "op",
		NeedsOp:  true,
		Help:     "/op <player> — grant operator privileges",
		Run:      cmdOp,
		Complete: playerArg(1),
	})
	server.RegisterCommand(&server.Command{
		Name:     "deop",
		NeedsOp:  true,
		Help:     "/deop <player> — revoke operator privileges",
		Run:      cmdDeop,
		Complete: playerArg(1),
	})
	server.RegisterCommand(&server.Command{
		Name:     "gamemode",
		Aliases:  []string{"gm"},
		NeedsOp:  true,
		Help:     "/gamemode <survival|creative|adventure|spectator> [player]",
		Run:      cmdGamemode,
		Complete: playerArg(2),
	})
	server.RegisterCommand(&server.Command{
		Name:     "effect",
		NeedsOp:  true,
		Help:     "/effect <player> <effect> [level] [seconds] | /effect <player> clear (speed, slowness, haste, mining_fatigue, jump_boost, regeneration, invisibility, blindness)",
		Run:      cmdEffect,
		Complete: completeEffect,
	})
	server.RegisterCommand(&server.Command{
		Name:     "tp",
		Aliases:  []string{"teleport"},
		NeedsOp:  true,
		Help:     "/tp <player> | /tp <x> <y> <z>",
		Run:      cmdTp,
		Complete: playerArg(1),
	})
}

func cmdOp(c *server.ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.SendMessage("Usage: /op <player>")
		return
	}
	target := args[0]
	c.Server().Ops.Add(target)
	_ = c.SendMessage("Granted op to " + target)
	if conn, _, ok := c.Server().FindPlayer(target); ok {
		_ = conn.SendMessage("You are now an operator")
	}
}

func cmdDeop(c *server.ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.SendMessage("Usage: /deop <player>")
		return
	}
	target := args[0]
	c.Server().Ops.Remove(target)
	_ = c.SendMessage("Revoked op from " + target)
	if conn, _, ok := c.Server().FindPlayer(target); ok {
		_ = conn.SendMessage("You are no longer an operator")
	}
}

// --- /gamemode ---

func cmdGamemode(c *server.ClientConnection, args []string) {
	if len(args) < 1 || len(args) > 2 {
		_ = c.SendMessage("Usage: /gamemode <survival|creative|adventure|spectator> [player]")
		return
	}
	mode, ok := command.ParseGamemode(args[0])
	if !ok {
		_ = c.SendMessage("Unknown gamemode: " + args[0])
		return
	}
	target := c
	if len(args) == 2 {
		conn, _, ok := c.Server().FindPlayer(args[1])
		if !ok {
			_ = c.SendMessage("Player not found: " + args[1])
			return
		}
		target = conn
	}
	target.SetGamemode(mode)
	_ = c.SendMessage(fmt.Sprintf("Set %s's gamemode to %s", target.Player().Name, args[0]))
	if target != c {
		_ = target.SendMessage("Your gamemode is now " + args[0])
	}
}

// --- /tp ---

func cmdTp(c *server.ClientConnection, args []string) {
	switch len(args) {
	case 1:
		// /tp <player> — teleport sender to target. Only works inside the
		// same instance for now — cross-instance teleport needs more wiring.
		target, inst, ok := c.Server().FindPlayer(args[0])
		if !ok {
			_ = c.SendMessage("Player not found: " + args[0])
			return
		}
		if inst != c.Instance() {
			_ = c.SendMessage("Player is in another instance")
			return
		}
		s := target.Player().Snapshot()
		c.Teleport(s.X, s.Y, s.Z)
		_ = c.SendMessage("Teleported to " + target.Player().Name)
	case 3:
		// /tp <x> <y> <z>
		x, y, z, err := command.ParseCoords(args)
		if err != nil {
			_ = c.SendMessage(err.Error())
			return
		}
		c.Teleport(x, y, z)
		_ = c.SendMessage(fmt.Sprintf("Teleported to %.2f, %.2f, %.2f", x, y, z))
	default:
		_ = c.SendMessage("Usage: /tp <player>  or  /tp <x> <y> <z>")
	}
}

// --- /effect ---

// cmdEffect grants or clears a potion effect on a player (modifiers.go).
//
//	/effect <player> <effect> [level=1] [seconds=30]
//	/effect <player> clear
func cmdEffect(c *server.ClientConnection, args []string) {
	if len(args) < 2 {
		_ = c.SendMessage("Usage: /effect <player> <" + strings.Join(effect.Names(), "|") + "> [level] [seconds] | /effect <player> clear")
		return
	}
	target, _, ok := c.Server().FindPlayer(args[0])
	if !ok {
		_ = c.SendMessage("Player not online: " + args[0])
		return
	}
	if strings.EqualFold(args[1], "clear") {
		target.ClearEffects()
		_ = c.SendMessage("Cleared effects on " + target.Name())
		return
	}
	eff, ok := effect.Known[strings.ToLower(args[1])]
	if !ok {
		_ = c.SendMessage("Unknown effect: " + args[1] + " (" + strings.Join(effect.Names(), ", ") + ")")
		return
	}
	level, seconds := 1, 30
	if len(args) >= 3 {
		if v, err := strconv.Atoi(args[2]); err == nil && v >= 1 && v <= 255 {
			level = v
		} else {
			_ = c.SendMessage("Level must be 1..255")
			return
		}
	}
	if len(args) >= 4 {
		if v, err := strconv.Atoi(args[3]); err == nil && v >= 1 {
			seconds = v
		} else {
			_ = c.SendMessage("Seconds must be ≥1")
			return
		}
	}
	target.ApplyEffect(eff, level, time.Duration(seconds)*time.Second)
	_ = c.SendMessage(fmt.Sprintf("Gave %s %s %d for %ds", target.Name(), eff.Name, level, seconds))
}
