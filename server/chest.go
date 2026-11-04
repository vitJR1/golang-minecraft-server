package server

import (
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/gameplay/chest"
	"minecraft-server/server/gameplay/enderchest"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// chest.go gives chest blocks a stored, persistent inventory. Opening a chest
// streams its saved contents; the player rearranging items is simulated by
// the server (click.go) and the result stored. Regular chests store per (instance, block position); an ender chest
// is one 27-slot inventory per player, shared by every ender chest block in
// every instance (enderchest.Chests on Server). The storage is server/gameplay/chest
// and server/gameplay/enderchest; this file opens them. There's no item NBT model, so
// stacks are just (item id, count) plus the tool fields below.

// enderChestFor returns the player's ender chest, loading it on first use.
func (s *Server) enderChestFor(uuid [16]byte) *enderchest.Chest {
	return s.enderChests.For(s.EnderChests, uuid)
}

// saveEnderChest schedules e to be written through Server.EnderChests
// (after every click; the write happens in the background, coalesced —
// enderchest.Saver).
func (s *Server) saveEnderChest(e *enderchest.Chest) { s.enderSaver.Mark(s.EnderChests, e) }

// openBlockChest opens the chest block at pos for this client: a regular
// chest binds the menu to that position; an ender chest binds it to the
// player's own ender inventory. Then the stored contents are streamed.
func (c *ClientConnection) openBlockChest(pos world.Position) {
	var store chest.Store = c.instance.chests.At(pos)
	if chest.IsEnder(c.instance.World.GetBlock(pos)) && c.player != nil && c.server != nil {
		e := c.server.enderChestFor(c.player.UUID)
		if !e.Available() {
			// The stored contents couldn't be loaded: an empty window would
			// look like the items are gone.
			_ = c.sendSystemMessage("Ender chest unavailable, try again later.")
			return
		}
		store = e
	}
	p := pos
	c.openChest(&openMenu{kind: "chest", chest: store, inst: c.instance, at: &p})
}

// openChestStore shows store's contents in the chest GUI.
func (c *ClientConnection) openChestStore(store chest.Store) {
	c.openChest(&openMenu{kind: "chest", chest: store})
}

func (c *ClientConnection) openChest(m *openMenu) {
	store := m.chest
	c.drag.Reset()
	c.menu.Store(m)
	_ = c.sendOpenScreen(store.Title(), chest.Rows)
	_ = c.sendChestInventory(store.Contents())
}

// chestReach is vanilla's Container.stillValid distance: the player within 8
// blocks of the block's centre.
const chestReach = 8

// chestStillValid is vanilla's stillValid for a chest opened on a block:
// the player is still in that block's instance and within chestReach of it.
func (c *ClientConnection) chestStillValid(m *openMenu) bool {
	if m.at == nil {
		return true
	}
	if m.inst != c.instance || c.player == nil {
		return false
	}
	s := c.player.Snapshot()
	dx := s.X - (float64(m.at.X) + 0.5)
	dy := s.Y - (float64(m.at.Y) + 0.5)
	dz := s.Z - (float64(m.at.Z) + 0.5)
	return dx*dx+dy*dy+dz*dz <= chestReach*chestReach
}

// sendChestInventory streams a chest's contents (Set Container Content): the 27
// chest slots from `slots`, then the player's real inventory (main + hotbar
// mirror from c.inv), then the cursor.
func (c *ClientConnection) sendChestInventory(slots [inventory.ChestSlots]inventory.Stack) error {
	window := append(slots[:], c.inv.Mirror()...)
	return c.safeWrite(packetid.CbPlaySetContainerContent, inventory.ContentsPayload(menuWindowID, window, c.inv.Cursor()))
}
