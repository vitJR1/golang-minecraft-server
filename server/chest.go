package server

import (
	"bytes"

	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/chest"
	"minecraft-server/server/enderchest"
	"minecraft-server/server/inventory"
	"minecraft-server/world"
)

// chest.go gives chest blocks a stored, persistent inventory. Opening a chest
// streams its saved contents; the player rearranging items is simulated by
// the server (click.go) and the result stored. Regular chests store per (instance, block position); an ender chest
// is one 27-slot inventory per player, shared by every ender chest block in
// every instance (enderchest.Chests on Server). The storage is server/chest
// and server/enderchest; this file opens them. There's no item NBT model, so
// stacks are just (item id, count) plus the tool fields below.

// enderChestFor returns the player's ender chest, loading it on first use.
func (s *Server) enderChestFor(uuid [16]byte) *enderchest.Chest {
	return s.enderChests.For(s.EnderChests, uuid)
}

// saveEnderChest writes e through Server.EnderChests (after every click).
func (s *Server) saveEnderChest(e *enderchest.Chest) { enderchest.Save(s.EnderChests, e) }

// openBlockChest opens the chest block at pos for this client: a regular
// chest binds the menu to that position; an ender chest binds it to the
// player's own ender inventory. Then the stored contents are streamed.
func (c *ClientConnection) openBlockChest(pos world.Position) {
	var store chest.Store = c.instance.chests.At(pos)
	if chest.IsEnder(c.instance.World.GetBlock(pos)) && c.player != nil && c.server != nil {
		store = c.server.enderChestFor(c.player.UUID)
	}
	c.openChestStore(store)
}

// openChestStore shows store's contents in the chest GUI.
func (c *ClientConnection) openChestStore(store chest.Store) {
	c.drag.Reset()
	c.menu.Store(&openMenu{kind: "chest", chest: store})
	_ = c.sendOpenScreen(store.Title(), chest.Rows)
	_ = c.sendChestInventory(store.Contents())
}

// sendChestInventory streams a chest's contents (Set Container Content): the 27
// chest slots from `slots`, then the player's real inventory (main + hotbar
// mirror from c.inv), then the cursor.
func (c *ClientConnection) sendChestInventory(slots [inventory.ChestSlots]inventory.Stack) error {
	var buf bytes.Buffer
	buf.WriteByte(menuWindowID)
	protocol.WriteVarInt32ToBuffer(&buf, 0) // state id
	protocol.WriteVarInt32ToBuffer(&buf, int32(inventory.ChestSlots+36))
	for s := 0; s < inventory.ChestSlots; s++ {
		inventory.WriteSlot(&buf, slots[s])
	}
	c.writeInventoryMirror(&buf)
	inventory.WriteSlot(&buf, c.cursor)
	return c.safeWrite(packetid.CbPlaySetContainerContent, buf.Bytes())
}
