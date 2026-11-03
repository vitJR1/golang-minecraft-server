package server

import (
	"bytes"
	"encoding/json"

	"minecraft-server/game"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/gameplay/chest"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// menu.go: chest-window GUIs. A connection has at most one open (c.menu):
// a chest (block or ender, simulated clicks — click.go) or a menu whose
// clicks go to a callback (plugin GUIs, the hub's navigation — server/features/hub —
// through OpenMenu). Click Container and Close Container route here
// (handler_play_player.go).

// menuWindowID is the window id we hand the client when opening a chest.
// Stays constant per connection since only one menu can be open at a time
// (the client closes the previous one when we send a new Open Screen).
// Anything 1..127 works; 1 is conventional.
const menuWindowID byte = 1

// openMenu tracks which menu the connection currently has open and how to
// dispatch clicks. Nil-valued field on ClientConnection means no menu.
type openMenu struct {
	kind    string              // "chest" | menuKindPlugin
	entries map[int16]menuEntry // slot → entry
	onClick func(c *ClientConnection, e menuEntry, click menuClick)

	// chest is the open chest's storage when kind == "chest" (a block chest
	// or the player's ender chest), so Click Container changes persist there.
	chest chest.Store

	// rows is the window height, kept so a plugin menu can be re-sent
	// after each click.
	rows int
}

// menuClick is the button + mode pair of a Click Container packet, as the
// vanilla client sends it: mode 0 = plain click (button 0 left, 1 right),
// mode 1 = shift-click ("quick move"), 2 = number key, 3 = middle, 4 = drop,
// 5 = drag, 6 = double click.
type menuClick struct {
	button byte
	mode   int
}

// plugin converts the raw pair into the modifiers plugins see.
func (m menuClick) plugin(slot int) game.MenuClick {
	return game.MenuClick{Slot: slot, Shift: m.mode == 1, Right: m.mode == 0 && m.button == 1}
}

// menuKindPlugin marks a GUI opened through game.PlayerHandle.OpenMenu.
const menuKindPlugin = "plugin"

// openPluginMenu implements PlayerHandle.OpenMenu: a rows×9 chest window
// whose slots come from items and whose clicks go to onClick.
func (c *ClientConnection) openPluginMenu(title string, rows int, items []game.MenuItem, onClick func(click game.MenuClick)) {
	if rows < 1 {
		rows = 1
	}
	if rows > 6 {
		rows = 6
	}
	entries := map[int16]menuEntry{}
	for _, it := range items {
		if it.Slot < 0 || it.Slot >= rows*9 {
			continue
		}
		id, ok := world.ItemByName(it.Item)
		if !ok {
			continue
		}
		count := it.Count
		if count < 1 {
			count = 1
		}
		if count > 64 {
			count = 64
		}
		entries[int16(it.Slot)] = menuEntry{slot: int16(it.Slot), itemID: id, count: byte(count), name: it.Name, key: it.Name, lore: it.Lore, glint: it.Glint}
	}
	c.menu.Store(&openMenu{
		kind:    menuKindPlugin,
		rows:    rows,
		entries: entries,
		onClick: func(_ *ClientConnection, e menuEntry, click menuClick) {
			if onClick != nil {
				onClick(click.plugin(int(e.slot)))
			}
		},
	})
	_ = c.sendOpenScreen(title, rows)
	_ = c.sendChestContents(rows, entries)
}

type menuEntry struct {
	slot   int16
	itemID int32
	name   string   // display label and the string used in logs / dispatch
	key    string   // stable identifier for code paths (e.g. "ffa", "garden")
	count  byte     // item stack size (0 → 1); used to show player counts
	lore   []string // hover lines under the name (plugin menus)
	glint  bool     // enchanted shimmer (plugin menus)
}

// stack renders the entry as the item stack shown in the window.
func (e menuEntry) stack() inventory.Stack {
	count := e.count
	if count == 0 {
		count = 1
	}
	return inventory.Stack{ID: e.itemID, Count: count, Name: e.name, Lore: e.lore, Glint: e.glint}
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
	return c.safeWrite(packetid.CbPlayOpenScreen, buf.Bytes())
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
// then mirrors the player's real inventory (from the server-side model) so
// the client keeps rendering their items — navigator, resources, blocks —
// instead of treating the inventory as empty. entries may be nil for a plain
// (empty) chest.
func (c *ClientConnection) sendChestContents(rows int, entries map[int16]menuEntry) error {
	chestSlots := int16(rows * 9)

	window := make([]inventory.Stack, chestSlots, int(chestSlots)+36)
	for s := int16(0); s < chestSlots; s++ {
		if e, ok := entries[s]; ok {
			window[s] = e.stack()
		}
	}
	window = append(window, c.inv.Mirror()...)
	// No carried item: the menu's icons can't be picked up.
	return c.safeWrite(packetid.CbPlaySetContainerContent, inventory.ContentsPayload(menuWindowID, window, inventory.Stack{}))
}
