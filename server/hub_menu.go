package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"minecraft-server/protocol"
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
)

// hubBlazeRodSlot is the inventory slot we place the blaze rod into.
// Vanilla container layout: 36..44 are the hotbar, slot 36 is the
// first/leftmost cell. The client auto-selects hotbar 0 (= slot 36) on
// join so it's already in hand.
const hubBlazeRodSlot int16 = 36

// chest window types (minecraft.wiki "Open Screen" inventory types):
//
//	0 = generic_9x1, 2 = generic_9x3. We use 9x1 for everything since
//	neither the main menu (3 icons) nor an arena list (6 icons) needs
//	more than one row.
const chestType9x1 int32 = 0

// arenaWindowID is the window id we hand the client when opening a chest.
// Stays constant per connection since only one menu can be open at a time
// (the client closes the previous one when we send a new Open Screen).
// Anything 1..127 works; 1 is conventional.
const menuWindowID byte = 1

// openMenu tracks which menu the connection currently has open and how to
// dispatch clicks. Nil-valued field on ClientConnection means no menu.
type openMenu struct {
	kind    string              // "main" | "ffa" | "bw" | "sw"
	entries map[int16]menuEntry // slot → entry
	onClick func(c *ClientConnection, e menuEntry)
}

type menuEntry struct {
	slot   int16
	itemID int32
	name   string // display label and the string used in logs / dispatch
	key    string // stable identifier for code paths (e.g. "ffa", "garden")
}

// Per-game arena names. Six each, matching what the user asked for.
var (
	ffaArenas = []string{"The Pit", "Coliseum", "Sandbox", "Backalley", "Rooftop", "Tomb"}
	bwArenas  = []string{"Garden", "Aquarium", "Volcano", "Junkyard", "Stronghold", "Lighthouse"}
	swArenas  = []string{"Cumulus", "Stratos", "Nebula", "Vesper", "Aurora", "Eclipse"}
)

// SetupHubMenu wires the hub instance to give the blaze rod on join.
// Idempotent — calling more than once just reassigns the hook.
func SetupHubMenu(s *Server) {
	prev := s.Hub.OnPlayerJoin
	s.Hub.OnPlayerJoin = func(c *ClientConnection) {
		if prev != nil {
			prev(c)
		}
		giveBlazeRod(c)
	}
}

// giveBlazeRod puts a blaze rod into the player's hotbar via Set Container
// Slot with window=0 (player inventory). State ID 0 is acceptable for a
// first write — subsequent State ID enforcement only kicks in after the
// server sends Set Container Content.
func giveBlazeRod(c *ClientConnection) {
	var buf bytes.Buffer
	buf.WriteByte(0)                                // window id 0 = player inventory
	protocol.WriteVarInt32ToBuffer(&buf, 0)         // state id
	buf.Write(protocol.WriteShort(hubBlazeRodSlot)) // slot
	buf.Write(protocol.WriteSlotWithName(itemBlazeRod, 1, "Navigator"))
	if err := c.safeWrite(CbPlaySetContainerSlot, buf.Bytes()); err != nil {
		slog.Warn("give blaze rod failed", "player", c.playerName, "err", err)
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
	_ = c.sendOpenScreen("Choose a game")
	_ = c.sendChestContents(entries)
}

// openHubArenaMenu opens the 6-arena sub-menu for the selected game.
func (c *ClientConnection) openHubArenaMenu(gameKey string, arenas []string, title string) {
	entries := make(map[int16]menuEntry, len(arenas))
	for i, name := range arenas {
		entries[int16(i)] = menuEntry{
			slot: int16(i), itemID: itemCompass, name: name, key: gameKey,
		}
	}
	c.menu.Store(&openMenu{
		kind:    gameKey,
		entries: entries,
		onClick: hubArenaOnClick,
	})
	_ = c.sendOpenScreen(title)
	_ = c.sendChestContents(entries)
}

// hubMainOnClick: clicking a game icon opens the matching arena list.
func hubMainOnClick(c *ClientConnection, e menuEntry) {
	switch e.key {
	case "ffa":
		c.openHubArenaMenu("ffa", ffaArenas, "FFA arenas")
	case "bw":
		c.openHubArenaMenu("bw", bwArenas, "BedWars arenas")
	case "sw":
		c.openHubArenaMenu("sw", swArenas, "SkyWars arenas")
	}
}

// hubArenaOnClick: clicking an arena logs the selection + tells the player.
// Real matchmaker dispatch lands in a follow-up; for now this is the proof
// that the menu round-trip works.
func hubArenaOnClick(c *ClientConnection, e menuEntry) {
	slog.Info("hub menu: arena picked",
		"player", c.playerName, "game", e.key, "arena", e.name)
	_ = c.sendSystemMessage(fmt.Sprintf("You picked %s — %s", e.key, e.name))
	c.menu.Store(nil)
}

// sendOpenScreen tells the client to open a single-row chest GUI under
// the connection's reserved window id.
func (c *ClientConnection) sendOpenScreen(title string) error {
	titleJSON, _ := json.Marshal(map[string]string{"text": title})
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, int32(menuWindowID))
	protocol.WriteVarInt32ToBuffer(&buf, chestType9x1)
	buf.Write(protocol.WriteString(string(titleJSON)))
	return c.safeWrite(CbPlayOpenScreen, buf.Bytes())
}

// Chest window slot layout for generic_9x1:
//
//	0..8   = chest contents
//	9..35  = player main inventory (3 rows × 9)
//	36..44 = player hotbar (9 slots)
//
// Without re-emitting the player inventory in the chest's mirror, the
// client treats those 36 slots as authoritative-empty and the blaze rod
// vanishes the moment the chest opens. We sneak it back into hotbar 0
// (chest-window slot 36) so the client sees it stay across open/close.
const (
	chestSlots       = 9
	chestTotalSlots  = chestSlots + 36
	chestHotbarSlot0 = int16(chestSlots + 27) // = 36
)

// sendChestContents fills the open chest's 9 visible slots from entries,
// then mirrors the player's inventory in slots 9..44 so the client keeps
// rendering the blaze rod (and any future items) instead of treating the
// player inventory as empty.
func (c *ClientConnection) sendChestContents(entries map[int16]menuEntry) error {
	var buf bytes.Buffer
	buf.WriteByte(menuWindowID)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	protocol.WriteVarInt32ToBuffer(&buf, int32(chestTotalSlots))

	for s := int16(0); s < int16(chestTotalSlots); s++ {
		switch {
		case s == chestHotbarSlot0:
			// Player inventory mirror: blaze rod at hotbar 0.
			buf.Write(protocol.WriteSlotWithName(itemBlazeRod, 1, "Navigator"))
		default:
			if e, ok := entries[s]; ok {
				buf.Write(protocol.WriteSlotWithName(e.itemID, 1, e.name))
			} else {
				buf.Write(protocol.WriteEmptySlot())
			}
		}
	}
	buf.Write(protocol.WriteEmptySlot()) // carried item (cursor)

	return c.safeWrite(CbPlaySetContainerContent, buf.Bytes())
}
