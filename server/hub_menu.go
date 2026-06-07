package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"minecraft-server/protocol"
	"minecraft-server/templates"
	"minecraft-server/world"
	"strings"
)

// Hub navigation menu.
//
// On hub join the server gives the player a blaze rod in hot-bar slot 0.
// Right-clicking it (Sb Use Item, 0x32) opens a single-row chest GUI with
// 3 icons — FFA / BedWars / SkyWars. Clicking an icon opens a second
// menu listing 6 arenas for that game. Clicking an arena currently just
// logs the selection (and tells the player via system chat) — actual
// matchmaker dispatch comes later.
//
// Internally:
//   - openMenu on ClientConnection holds the current window id + slot →
//     action map.
//   - SbPlayUseItem (hub only) opens the main menu.
//   - SbPlayClickContainer dispatches by slot.
//   - SbPlayCloseContainer clears the state.

// Item IDs from minecraft-data 1.20 items.json — same as the player will
// see if they type /give. Picked icons match each game's vibe.
const (
	itemBlazeRod     = 953
	itemDiamondSword = 797 // FFA icon
	itemRedBed       = 938 // BedWars icon
	itemFeather      = 811 // SkyWars icon
	itemCompass      = 888 // arena icon (all 6 per game)
	itemEnderPearl   = 952 // "Arena selector" — handed out in lobbies
)

// Hotbar slots in the player-inventory container layout. 36..44 are the
// 9 hotbar cells, 36 being the leftmost. Hub gives the blaze rod into
// slot 0; lobbies additionally drop an ender pearl into slot 1 so the
// player can swap to it (1 key) to open the arena picker.
const (
	hotbarSlot0 int16 = 36 // navigator (blaze rod)
	hotbarSlot1 int16 = 37 // arena selector (ender pearl, lobby only)
)

// arenaWindowID is the window id we hand the client when opening a chest.
// Stays constant per connection since only one menu can be open at a time
// (the client closes the previous one when we send a new Open Screen).
// Anything 1..127 works; 1 is conventional.
const menuWindowID byte = 1

// openMenu tracks which menu the connection currently has open and how to
// dispatch clicks. Nil-valued field on ClientConnection means no menu.
type openMenu struct {
	kind    string              // "main" | "ffa" | "bw" | "sw" | "chest"
	entries map[int16]menuEntry // slot → entry
	onClick func(c *ClientConnection, e menuEntry)

	// chestPos is the block position of the open chest when kind == "chest",
	// so Click Container changes persist to the right chest.
	chestPos world.Position
}

type menuEntry struct {
	slot   int16
	itemID int32
	name   string // display label and the string used in logs / dispatch
	key    string // stable identifier for code paths (e.g. "ffa", "garden")
	count  byte   // item stack size (0 → 1); used to show player counts
}

// hubMenuTargets maps a menu icon's `key` to the lobby instance ID the
// click teleports the player into. Mirror LobbyFFA / LobbyBedWars /
// LobbySkyWars from lobbies.go.
var hubMenuTargets = map[string]string{
	"ffa": LobbyFFA,
	"bw":  LobbyBedWars,
	"sw":  LobbySkyWars,
}

// lobbyArenas maps each lobby instance ID to the list of arena names
// shown in that lobby's ender-pearl menu. Membership in this map also
// doubles as "is this instance a game lobby" — see arenasForLobby.
//
// Names are placeholders; future matchmaker dispatch will look them up
// in the game's Definition.
var lobbyArenas = map[string][]string{
	LobbyFFA:     {"The Pit", "Coliseum", "Sandbox", "Backalley", "Rooftop", "Tomb"},
	LobbyBedWars: {"Garden", "Aquarium", "Volcano", "Junkyard", "Stronghold", "Lighthouse"},
	LobbySkyWars: {"Cumulus", "Stratos", "Nebula", "Vesper", "Aurora", "Eclipse"},
}

// arenasForLobby returns (arenas, true) if instanceID is a game lobby,
// else (nil, false). The boolean is also the predicate "should this
// instance hand out an ender-pearl arena selector".
func arenasForLobby(instanceID string) ([]string, bool) {
	arenas, ok := lobbyArenas[instanceID]
	return arenas, ok
}

// SetupHubMenu wires the hub instance: gives the blaze rod on join and
// installs the protection vetos (no block break/place, no PvP). The hub
// is a navigation lobby — players go to a game instance to actually
// build or fight. Idempotent — calling more than once just reassigns
// the hooks.
func SetupHubMenu(s *Server) {
	prev := s.Hub.OnPlayerJoin
	s.Hub.OnPlayerJoin = func(c *ClientConnection) {
		if prev != nil {
			prev(c)
		}
		giveBlazeRod(c)
	}

	// Protection. The vetos work regardless of gamemode — creative
	// "instant break" (action=0) still routes through OnBlockBreak and
	// gets bounced. The server replies with a Block Update carrying the
	// original block so the client rolls back its prediction; combined
	// with the system message the player gets immediate feedback.
	s.Hub.OnBlockBreak = func(c *ClientConnection, _ world.Position) bool {
		_ = c.sendSystemMessage("Hub is protected — join a game to build.")
		return false
	}
	s.Hub.OnBlockPlace = func(c *ClientConnection, _ world.Position, _ world.Block) bool {
		_ = c.sendSystemMessage("Hub is protected — join a game to build.")
		return false
	}
	// PvP-off. There's no damage system yet so the attack interaction is
	// effectively a no-op either way, but returning false here documents
	// the policy and gives a place to hang knockback / health rollback
	// once those exist. Silent veto — attack swings are spammy and we
	// don't want chat flooded.
	s.Hub.OnPlayerAttack = func(_ *ClientConnection, _ *ClientConnection) bool {
		return false
	}
}

// giveBlazeRod puts the "Navigator" blaze rod into the player's hotbar
// slot 0. Used by hub + lobby OnPlayerJoin so it's always available.
// State ID 0 is fine for a first write — vanilla's State ID checks
// only kick in after the server sends Set Container Content.
func giveBlazeRod(c *ClientConnection) {
	giveHotbarItem(c, hotbarSlot0, itemBlazeRod, "Navigator")
}

// giveArenaSelector puts an "Arena selector" ender pearl into hotbar
// slot 1. Lobbies call this so the player can press `2` and right-click
// to open the per-game arena picker.
func giveArenaSelector(c *ClientConnection) {
	giveHotbarItem(c, hotbarSlot1, itemEnderPearl, "Arena selector")
}

// giveHotbarItem is the underlying wire push used by giveBlazeRod /
// giveArenaSelector. Pushes a single Set Container Slot at window 0
// (player inventory).
func giveHotbarItem(c *ClientConnection, slot int16, itemID int32, name string) {
	var buf bytes.Buffer
	buf.WriteByte(0)                        // window id 0 = player inventory
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	buf.Write(protocol.WriteShort(slot))
	buf.Write(protocol.WriteSlotWithName(itemID, 1, name))
	if err := c.safeWrite(CbPlaySetContainerSlot, buf.Bytes()); err != nil {
		slog.Warn("give hotbar item failed",
			"player", c.playerName, "item", name, "err", err)
	}
}

// openHubMainMenu sends Open Screen + Set Container Content for the
// game-picker. Called when the player right-clicks the blaze rod inside
// the hub.
func (c *ClientConnection) openHubMainMenu() {
	entries := map[int16]menuEntry{
		2: {slot: 2, itemID: itemDiamondSword, name: "Free-For-All", key: "ffa"},
		4: {slot: 4, itemID: itemRedBed, name: "BedWars", key: "bw"},
		6: {slot: 6, itemID: itemFeather, name: "SkyWars", key: "sw"},
	}
	c.menu.Store(&openMenu{
		kind:    "main",
		entries: entries,
		onClick: hubMainOnClick,
	})
	_ = c.sendOpenScreen("Choose a game", 1)
	_ = c.sendChestContents(1, entries)
}

// openArenaMenu opens the per-lobby chest GUI listing arenas to pick.
// Called by SbPlayUseItem when the player right-clicks the ender pearl
// (slot 1) while standing in a game lobby.
func (c *ClientConnection) openArenaMenu(lobbyID string, arenas []string) {
	entries := make(map[int16]menuEntry, len(arenas))
	for i, name := range arenas {
		entries[int16(i)] = menuEntry{
			slot: int16(i), itemID: itemCompass, name: name, key: lobbyID,
		}
	}
	c.menu.Store(&openMenu{
		kind:    lobbyID + "-arenas",
		entries: entries,
		onClick: arenaOnClick,
	})
	_ = c.sendOpenScreen(strings.ToUpper(lobbyID[:1])+lobbyID[1:]+" arenas", 1)
	_ = c.sendChestContents(1, entries)
}

// arenaOnClick: arena slot click — logs + system-chat the choice and
// closes the server-side menu. Future hook: matchmaker.Queue or
// teleport into the arena instance itself.
func arenaOnClick(c *ClientConnection, e menuEntry) {
	slog.Info("arena picked",
		"player", c.playerName, "lobby", e.key, "arena", e.name)
	_ = c.sendSystemMessage("You picked " + e.key + " — " + e.name)
	c.menu.Store(nil)
}

// openBedwarsArenaMenu shows the live DOTA arena browser: a "create new arena"
// compass plus one compass per running bedwars arena that already has players
// (its stack size = player count). Clicking creates-and-joins or joins.
func (c *ClientConnection) openBedwarsArenaMenu() {
	entries := bedwarsArenaEntries(c.server)
	c.menu.Store(&openMenu{kind: "bw-arenas", entries: entries, onClick: bedwarsArenaOnClick})
	_ = c.sendOpenScreen("DOTA arenas", 1)
	_ = c.sendChestContents(1, entries)
}

// bedwarsArenaEntries builds the DOTA arena browser slots: slot 0 is always
// "create a new arena"; each following slot is a running bedwars arena that has
// players, its compass stack size showing the online count.
func bedwarsArenaEntries(s *Server) map[int16]menuEntry {
	entries := map[int16]menuEntry{
		0: {slot: 0, itemID: itemCompass, name: "+ New DOTA arena", key: "create", count: 1},
	}
	slot := int16(1)
	for _, name := range s.ArenasOfKind("bedwars") {
		inst := s.GetInstance(name)
		if inst == nil {
			continue
		}
		n := inst.Players.Count()
		if n <= 0 {
			continue // only list arenas someone's already on
		}
		count := byte(n)
		if n > 64 {
			count = 64 // item stacks cap at 64
		}
		entries[slot] = menuEntry{
			slot: slot, itemID: itemCompass, count: count,
			name: fmt.Sprintf("DOTA %s — %d online", name, n), key: name,
		}
		slot++
	}
	return entries
}

// bedwarsArenaOnClick handles the DOTA arena browser: "create" spins up a fresh
// arena from the DOTA map and joins it; any other key is an existing arena name
// to join directly.
func bedwarsArenaOnClick(c *ClientConnection, e menuEntry) {
	c.menu.Store(nil)
	name := e.key
	if name == "create" {
		created, err := c.server.CreateArena("bedwars", templates.BedwarsDotaMap, "")
		if err != nil {
			_ = c.sendSystemMessage("Couldn't create arena: " + err.Error())
			slog.Warn("bedwars arena create failed", "player", c.playerName, "err", err)
			return
		}
		name = created
	}
	c.joinArena(name)
}

// joinArena moves the player into a running arena instance by name. Runs on the
// player's readLoop (menu click / command handler).
func (c *ClientConnection) joinArena(name string) {
	target := c.server.GetInstance(name)
	if target == nil {
		_ = c.sendSystemMessage("Arena " + name + " is not running.")
		return
	}
	if target == c.instance {
		return
	}
	if err := c.server.MovePlayer(c, target, 0, 80, 0); err != nil {
		_ = c.sendSystemMessage("Couldn't join " + name + ": " + err.Error())
		slog.Warn("join arena failed", "player", c.playerName, "arena", name, "err", err)
		return
	}
	_ = c.sendSystemMessage("Joined arena " + name)
}

// hubMainOnClick: clicking a game icon (FFA / BedWars / SkyWars)
// teleports the player into the matching lobby instance. The chest GUI
// is dismissed implicitly by the Respawn that MovePlayer sends; we
// only have to clear the server-side menu state.
func hubMainOnClick(c *ClientConnection, e menuEntry) {
	lobbyID, ok := hubMenuTargets[e.key]
	if !ok {
		return
	}
	target := c.server.GetInstance(lobbyID)
	if target == nil {
		_ = c.sendSystemMessage("Lobby unavailable: " + lobbyID)
		slog.Warn("hub menu: lobby not registered",
			"player", c.playerName, "lobby", lobbyID)
		return
	}
	c.menu.Store(nil)
	if err := c.server.MovePlayer(c, target, 0.5, 67, 0.5); err != nil {
		_ = c.sendSystemMessage("Couldn't enter lobby: " + err.Error())
		slog.Warn("hub menu: move failed",
			"player", c.playerName, "lobby", lobbyID, "err", err)
		return
	}
	slog.Info("hub menu: entered lobby",
		"player", c.playerName, "lobby", lobbyID)
}

// sendOpenScreen tells the client to open a chest GUI of `rows` rows (1..6)
// under the connection's reserved window id. The window type enum is rows-1
// (0 = generic_9x1 … 5 = generic_9x6).
func (c *ClientConnection) sendOpenScreen(title string, rows int) error {
	titleJSON, _ := json.Marshal(map[string]string{"text": title})
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, int32(menuWindowID))
	protocol.WriteVarInt32ToBuffer(&buf, int32(rows-1))
	buf.Write(protocol.WriteString(string(titleJSON)))
	return c.safeWrite(CbPlayOpenScreen, buf.Bytes())
}

// Chest window slot layout for a generic_9x`rows` chest:
//
//	0 .. rows*9-1          = chest contents
//	rows*9 .. rows*9+26    = player main inventory (3 rows × 9)
//	rows*9+27 .. +35       = player hotbar (9 slots)
//
// Without re-emitting the player inventory in the chest's mirror, the
// client treats those 36 slots as authoritative-empty and the blaze rod
// vanishes the moment the chest opens. We sneak it back into the mirrored
// hotbar so the client sees it stay across open/close.

// sendChestContents fills the open chest's rows*9 visible slots from entries,
// then mirrors the player's hotbar so the client keeps rendering the navigator
// (and the arena selector in lobbies) instead of treating the inventory as
// empty. entries may be nil for a plain (empty) chest.
func (c *ClientConnection) sendChestContents(rows int, entries map[int16]menuEntry) error {
	chestSlots := int16(rows * 9)
	total := chestSlots + 36
	hotbarSlot0 := chestSlots + 27
	hotbarSlot1 := chestSlots + 28

	var buf bytes.Buffer
	buf.WriteByte(menuWindowID)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	protocol.WriteVarInt32ToBuffer(&buf, int32(total))

	// Are we in a game lobby? If yes, include the ender pearl in the
	// mirror so a chest-open-then-close cycle doesn't wipe it.
	includePearl := false
	if c.instance != nil {
		_, includePearl = arenasForLobby(c.instance.ID)
	}

	for s := int16(0); s < total; s++ {
		switch {
		case s == hotbarSlot0:
			buf.Write(protocol.WriteSlotWithName(itemBlazeRod, 1, "Navigator"))
		case s == hotbarSlot1 && includePearl:
			buf.Write(protocol.WriteSlotWithName(itemEnderPearl, 1, "Arena selector"))
		default:
			if e, ok := entries[s]; ok {
				count := e.count
				if count == 0 {
					count = 1
				}
				buf.Write(protocol.WriteSlotWithName(e.itemID, count, e.name))
			} else {
				buf.Write(protocol.WriteEmptySlot())
			}
		}
	}
	buf.Write(protocol.WriteEmptySlot()) // carried item (cursor)

	return c.safeWrite(CbPlaySetContainerContent, buf.Bytes())
}
