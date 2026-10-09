package server

import (
	"fmt"
	"strconv"
	"strings"

	"minecraft-server/player"
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
	registerCommand(&Command{
		Name:    "bwgive",
		NeedsOp: true,
		Help:    "/bwgive <iron|gold|diamond|emerald> [n] — give yourself BedWars currency (arena only)",
		Run:     cmdBwGive,
	})
	registerCommand(&Command{
		Name:    "bwkill",
		NeedsOp: true,
		Help:    "/bwkill — kill yourself to test the death/respawn flow (arena only)",
		Run:     cmdBwKill,
	})
}

// inBedwarsArena reports whether the caller's instance runs with weapon
// damage (the BedWars switch), replying when it doesn't.
func inBedwarsArena(c *ClientConnection) bool {
	if c.instance == nil || !c.instance.WeaponDamage() {
		_ = c.sendSystemMessage("Only inside a BedWars arena.")
		return false
	}
	return true
}

func cmdBwGive(c *ClientConnection, args []string) {
	if len(args) < 1 || len(args) > 2 {
		_ = c.sendSystemMessage("Usage: /bwgive <iron|gold|diamond|emerald> [n]")
		return
	}
	if !inBedwarsArena(c) {
		return
	}
	item, ok := bwCurrencies[strings.ToLower(args[0])]
	if !ok {
		_ = c.sendSystemMessage("Unknown currency: " + args[0] + " (iron, gold, diamond, emerald)")
		return
	}
	n := 16
	if len(args) == 2 {
		v, err := strconv.Atoi(args[1])
		if err != nil || v < 1 || v > 64*36 {
			_ = c.sendSystemMessage("Amount must be 1..2304")
			return
		}
		n = v
	}
	id, _ := world.ItemByName(item)
	left := c.giveItem(id, n)
	_ = c.sendSystemMessage(fmt.Sprintf("Gave %d %s (%d didn't fit)", n-left, strings.ToLower(args[0]), left))
}

func cmdBwKill(c *ClientConnection, _ []string) {
	if !inBedwarsArena(c) {
		return
	}
	if c.player == nil || c.player.IsDead() {
		_ = c.sendSystemMessage("You're already dead.")
		return
	}
	// Lethal environmental damage: goes through armor like anything else,
	// but far beyond what armor can absorb, then the regular die() path.
	c.hurtEnvironment(player.MaxHealth*50, 0, c.instance.Tick())
}
