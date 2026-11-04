package hub

import (
	"fmt"
	"log/slog"
	"strings"

	"minecraft-server/game"
	"minecraft-server/server"
	"minecraft-server/templates"
)

// arenas.go: the lobbies' arena selector — a placeholder list in most
// lobbies, the live DOTA arena browser in the BedWars one.

// openArenas opens the per-lobby chest GUI listing arenas to pick.
func openArenas(c *server.ClientConnection, lobbyID string, arenas []string) {
	items := make([]game.MenuItem, 0, len(arenas))
	for i, name := range arenas {
		items = append(items, game.MenuItem{Slot: i, Item: "minecraft:compass", Name: name})
	}
	c.OpenMenu(strings.ToUpper(lobbyID[:1])+lobbyID[1:]+" arenas", 1, items, func(click game.MenuClick) {
		if click.Slot < 0 || click.Slot >= len(arenas) {
			return
		}
		// The choice is only logged and echoed for now; future hook:
		// matchmaker.Queue or a teleport into the arena itself.
		name := arenas[click.Slot]
		slog.Info("arena picked", "player", c.Name(), "lobby", lobbyID, "arena", name)
		_ = c.SendMessage("You picked " + lobbyID + " — " + name)
		c.CloseMenu()
	})
}

// Bedwars arena kinds (mirror the ArenaBuilder registrations in
// games/bedwars/arena_config.go): the regular team mode and the 1×1 duel.
const (
	bedwarsKindFull = "bedwars"
	bedwarsKindDuel = "bedwars-1x1"
)

// bedwarsMode describes one "create" button in the BedWars arena browser
// and how running arenas of that kind are labelled.
type bedwarsMode struct {
	kind   string // arena kind handed to CreateArena / ArenasOfKind
	create string // label of the "+ New …" button
	label  string // prefix for running arenas ("DOTA 4×4 bw-1 — 2 online")
	icon   string // item shown on the create button
}

// bedwarsModes is the ordered list of modes offered in the browser; create
// buttons take the first len(bedwarsModes) slots.
var bedwarsModes = []bedwarsMode{
	{kind: bedwarsKindFull, create: "+ New DOTA arena (4×4)", label: "DOTA 4×4", icon: "minecraft:red_bed"},
	{kind: bedwarsKindDuel, create: "+ New 1×1 duel", label: "Duel 1×1", icon: "minecraft:diamond_sword"},
}

// createKey is the key of a mode's "+ New …" button.
func (m bedwarsMode) createKey() string { return "create:" + m.kind }

// arenaEntry is one slot of the browser: the item shown and its key — a
// "create:<kind>" button or a running arena's name.
type arenaEntry struct {
	item game.MenuItem
	key  string
}

// bedwarsArenaEntries builds the DOTA arena browser slots: the first slots
// are the per-mode "create a new arena" buttons; each following slot is a
// running bedwars arena (any mode) that has players, its compass stack size
// showing the online count. arenas lists a kind's arenas, online counts an
// arena's players (-1 when it isn't running).
func bedwarsArenaEntries(arenas func(kind string) []string, online func(name string) int) []arenaEntry {
	var entries []arenaEntry
	for _, m := range bedwarsModes {
		entries = append(entries, arenaEntry{
			item: game.MenuItem{Slot: len(entries), Item: m.icon, Name: m.create, Count: 1},
			key:  m.createKey(),
		})
	}
	for _, m := range bedwarsModes {
		for _, name := range arenas(m.kind) {
			n := online(name)
			if n <= 0 {
				continue // only list arenas someone's already on
			}
			entries = append(entries, arenaEntry{
				item: game.MenuItem{
					Slot: len(entries), Item: "minecraft:compass", Count: min(n, 64), // stacks cap at 64
					Name: fmt.Sprintf("%s %s — %d online", m.label, name, n),
				},
				key: name,
			})
		}
	}
	return entries
}

// openBedwarsArenas shows the live DOTA arena browser. A "create:<kind>"
// click spins up a fresh arena of that kind from the DOTA map and joins it;
// any other entry is an existing arena to join.
func openBedwarsArenas(c *server.ClientConnection) {
	s := c.Server()
	entries := bedwarsArenaEntries(s.ArenasOfKind, func(name string) int {
		if inst := s.GetInstance(name); inst != nil {
			return inst.Players.Count()
		}
		return -1
	})
	items := make([]game.MenuItem, len(entries))
	for i, e := range entries {
		items[i] = e.item
	}
	c.OpenMenu("DOTA arenas", 1, items, func(click game.MenuClick) {
		if click.Slot < 0 || click.Slot >= len(entries) {
			return
		}
		c.CloseMenu()
		name := entries[click.Slot].key
		if kind, ok := strings.CutPrefix(name, "create:"); ok {
			created, err := c.Server().CreateArena(kind, templates.BedwarsDotaMap, "")
			if err != nil {
				_ = c.SendMessage("Couldn't create arena: " + err.Error())
				slog.Warn("bedwars arena create failed", "player", c.Name(), "kind", kind, "err", err)
				return
			}
			name = created
		}
		joinArena(c, name)
	})
}

// joinArena moves the player into a running arena instance by name. Runs on
// the player's readLoop (menu click).
func joinArena(c *server.ClientConnection, name string) {
	target := c.Server().GetInstance(name)
	if target == nil {
		_ = c.SendMessage("Arena " + name + " is not running.")
		return
	}
	if target == c.Instance() {
		return
	}
	if err := c.Server().Join(c, target); err != nil {
		_ = c.SendMessage("Couldn't join " + name + ": " + err.Error())
		slog.Warn("join arena failed", "player", c.Name(), "arena", name, "err", err)
		return
	}
	_ = c.SendMessage("Joined arena " + name)
}
