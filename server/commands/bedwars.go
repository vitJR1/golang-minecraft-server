package commands

import (
	"fmt"
	"strconv"
	"strings"

	"minecraft-server/server"
	"minecraft-server/world"
)

// bw_commands.go: op-only QA helpers for hand-testing BedWars-style rounds.
// Both only work inside an instance that runs with weapon damage on (a
// BedWars arena), so they can't be used to cheat in FFA or the hub.
//
//	/bwgive <iron|gold|diamond|emerald> [n]   give yourself currency (default 16)
//	/bwkill                                   kill yourself through the normal death path

var bwCurrencies = map[string]string{
	"iron":    "minecraft:iron_ingot",
	"gold":    "minecraft:gold_ingot",
	"diamond": "minecraft:diamond",
	"emerald": "minecraft:emerald",
}

func init() {
	server.RegisterCommand(&server.Command{
		Name:     "bwgive",
		NeedsOp:  true,
		Help:     "/bwgive <iron|gold|diamond|emerald> [n] — give yourself BedWars currency (arena only)",
		Run:      cmdBwGive,
		Complete: fixedArg(1, "iron", "gold", "diamond", "emerald"),
	})
	server.RegisterCommand(&server.Command{
		Name:    "bwkill",
		NeedsOp: true,
		Help:    "/bwkill — kill yourself to test the death/respawn flow (arena only)",
		Run:     cmdBwKill,
	})
}

// inBedwarsArena reports whether the caller's instance runs with weapon
// damage (the BedWars switch), replying when it doesn't.
func inBedwarsArena(c *server.ClientConnection) bool {
	if c.Instance() == nil || !c.Instance().WeaponDamage() {
		_ = c.SendMessage("Only inside a BedWars arena.")
		return false
	}
	return true
}

func cmdBwGive(c *server.ClientConnection, args []string) {
	if len(args) < 1 || len(args) > 2 {
		_ = c.SendMessage("Usage: /bwgive <iron|gold|diamond|emerald> [n]")
		return
	}
	if !inBedwarsArena(c) {
		return
	}
	item, ok := bwCurrencies[strings.ToLower(args[0])]
	if !ok {
		_ = c.SendMessage("Unknown currency: " + args[0] + " (iron, gold, diamond, emerald)")
		return
	}
	n := 16
	if len(args) == 2 {
		v, err := strconv.Atoi(args[1])
		if err != nil || v < 1 || v > 64*36 {
			_ = c.SendMessage("Amount must be 1..2304")
			return
		}
		n = v
	}
	id, _ := world.ItemByName(item)
	left := c.GiveItem(id, n)
	_ = c.SendMessage(fmt.Sprintf("Gave %d %s (%d didn't fit)", n-left, strings.ToLower(args[0]), left))
}

func cmdBwKill(c *server.ClientConnection, _ []string) {
	if !inBedwarsArena(c) {
		return
	}
	if c.Player() == nil || c.Player().IsDead() {
		_ = c.SendMessage("You're already dead.")
		return
	}
	c.Kill()
}
