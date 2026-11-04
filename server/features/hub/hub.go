// Package hub is the server's navigation: the hub instance's protection and
// its navigator (a blaze rod whose menu picks a game's lobby), the per-mode
// lobbies (read-only, with an arena selector), and the arena menus — the
// BedWars lobby's live DOTA arena browser among them. It is built over the
// server's exported API; main calls Setup and SetupLobbies.
package hub

import (
	"log/slog"

	"minecraft-server/game"
	"minecraft-server/server"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/server/instances/lobby"
	"minecraft-server/world"
)

// Hotbar slots in the player-inventory container layout. 36..44 are the
// 9 hotbar cells, 36 being the leftmost. The hub gives the blaze rod into
// slot 0; lobbies additionally drop an ender pearl into slot 1 so the
// player can swap to it (1 key) to open the arena picker.
const (
	navigatorSlot int16 = 36
	selectorSlot  int16 = 37
)

// Setup wires the hub instance: gives the navigator on join, installs the
// protection vetos (no block break/place, no PvP) and opens the menus when
// a menu item is used. The hub is a navigation lobby — players go to a game
// instance to actually build or fight. Idempotent — calling more than once
// just reassigns the hooks.
func Setup(s *server.Server) {
	prev := s.Hub.OnPlayerJoin
	s.Hub.OnPlayerJoin = func(c *server.ClientConnection) {
		if prev != nil {
			prev(c)
		}
		giveNavigator(c)
	}

	// Protection. The vetos work regardless of gamemode — creative
	// "instant break" (action=0) still routes through OnBlockBreak and
	// gets bounced. The server replies with a Block Update carrying the
	// original block so the client rolls back its prediction; combined
	// with the system message the player gets immediate feedback.
	s.Hub.OnBlockBreak = func(c *server.ClientConnection, _ world.Position) bool {
		_ = c.SendMessage("Hub is protected — join a game to build.")
		return false
	}
	s.Hub.OnBlockPlace = func(c *server.ClientConnection, _ world.Position, _ world.Block) bool {
		_ = c.SendMessage("Hub is protected — join a game to build.")
		return false
	}
	// PvP off, silently — attack swings are spammy and we don't want chat
	// flooded.
	s.Hub.OnPlayerAttack = func(_, _ *server.ClientConnection) bool { return false }

	s.MenuItems = useMenuItem
}

// useMenuItem opens the menu behind a right-clicked menu item: the
// navigator's game picker in the hub and any lobby, the selector's arena
// list in a lobby (the BedWars lobby's is the live DOTA arena browser).
func useMenuItem(c *server.ClientConnection, item string) {
	inst := c.Instance()
	switch item {
	case inventory.NavigatorName:
		if inst == c.Server().Hub || lobby.Is(inst.ID) {
			openMain(c)
		}
	case inventory.SelectorName:
		if inst.ID == lobby.BedWars {
			openBedwarsArenas(c)
		} else if arenas, ok := lobby.Arenas(inst.ID); ok {
			openArenas(c, inst.ID, arenas)
		}
	}
}

// giveNavigator puts the "Navigator" blaze rod into hotbar slot 0 (hub and
// lobby joins, so it's always available).
func giveNavigator(c *server.ClientConnection) {
	giveItem(c, navigatorSlot, "minecraft:blaze_rod", inventory.NavigatorName)
}

// giveSelector puts the "Arena selector" ender pearl into hotbar slot 1
// (lobbies), so the player can press `2` and right-click for the arena
// picker.
func giveSelector(c *server.ClientConnection) {
	giveItem(c, selectorSlot, "minecraft:ender_pearl", inventory.SelectorName)
}

func giveItem(c *server.ClientConnection, slot int16, item, name string) {
	id, _ := world.ItemByName(item)
	c.GiveHotbarItem(slot, id, name)
}

// mainItems is the game picker: the icon → lobby key (lobby.MenuTarget).
var mainItems = []struct {
	slot      int
	item, key string
	name      string
}{
	{2, "minecraft:diamond_sword", "ffa", "Free-For-All"},
	{4, "minecraft:red_bed", "bw", "BedWars"},
	{6, "minecraft:feather", "sw", "SkyWars"},
}

// openMain shows the game picker (navigator, in the hub and the lobbies).
func openMain(c *server.ClientConnection) {
	items := make([]game.MenuItem, 0, len(mainItems))
	keys := map[int]string{}
	for _, m := range mainItems {
		items = append(items, game.MenuItem{Slot: m.slot, Item: m.item, Name: m.name})
		keys[m.slot] = m.key
	}
	c.OpenMenu("Choose a game", 1, items, func(click game.MenuClick) {
		if key, ok := keys[click.Slot]; ok {
			enterLobby(c, key)
		}
	})
}

// enterLobby: clicking a game icon (FFA / BedWars / SkyWars) teleports the
// player into the matching lobby instance. The chest GUI is dismissed
// implicitly by the Respawn that MovePlayer sends; we only have to forget
// the menu server-side.
func enterLobby(c *server.ClientConnection, key string) {
	lobbyID, ok := lobby.MenuTarget(key)
	if !ok {
		return
	}
	target := c.Server().GetInstance(lobbyID)
	if target == nil {
		_ = c.SendMessage("Lobby unavailable: " + lobbyID)
		slog.Warn("hub menu: lobby not registered", "player", c.Name(), "lobby", lobbyID)
		return
	}
	c.CloseMenu()
	if err := c.Server().Join(c, target); err != nil {
		_ = c.SendMessage("Couldn't enter lobby: " + err.Error())
		slog.Warn("hub menu: move failed", "player", c.Name(), "lobby", lobbyID, "err", err)
		return
	}
	slog.Info("hub menu: entered lobby", "player", c.Name(), "lobby", lobbyID)
}
