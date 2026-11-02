package server

import (
	"bytes"
	"fmt"
	"log/slog"
	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/server/enderchest"
	"minecraft-server/server/inventory"
	"minecraft-server/server/packets"
)

// handler_play_player.go: the play packets about the player — movement,
// input state, the held slot, chat, inventory windows.

// onMove handles Set Player Position / Position and Rotation / Rotation:
// non-finite values kick; while a server teleport is unconfirmed, or when
// the look / move fails its check, the packet is dropped.
func (c *ClientConnection) onMove(m packets.Move) {
	if (m.HasPos && !anticheat.Finite64(m.X, m.Y, m.Z)) || (m.HasLook && !anticheat.Finite32(m.Yaw, m.Pitch)) {
		c.kickInvalidMove()
		return
	}
	if c.awaitingTeleport() || (m.HasLook && !c.acceptLook(m.Pitch)) || (m.HasPos && !c.acceptMove(m.X, m.Y, m.Z)) {
		return
	}
	switch {
	case m.HasPos && m.HasLook:
		c.rememberLook()
		c.player.MoveAndLook(m.X, m.Y, m.Z, m.Yaw, m.Pitch, m.OnGround)
	case m.HasPos:
		c.player.MoveTo(m.X, m.Y, m.Z, m.OnGround)
	default:
		c.rememberLook()
		c.player.LookAt(m.Yaw, m.Pitch, m.OnGround)
	}
	c.broadcastEntityTeleport()
	if m.HasLook {
		c.broadcastHeadRotation()
	}
}

// onPlayerCommand handles Player Command. We track sneak and sprint input —
// combat applies the 1.9 sprint-knockback bonus, anticheat keeps the sneak
// state in the position history; the rest is ignored for now.
func (c *ClientConnection) onPlayerCommand(pc packets.PlayerCommand) {
	// The vanilla client only ever names itself.
	if pc.EntityID != c.player.EntityID {
		c.flag("badpackets.command", fmt.Sprintf("player command for entity %d", pc.EntityID))
		return
	}
	switch pc.Action {
	case packets.CommandStartSneak:
		c.ac.Sneaking.Store(true)
	case packets.CommandStopSneak:
		c.ac.Sneaking.Store(false)
	case packets.CommandStartSprint:
		c.ac.Sprinting.Store(true)
	case packets.CommandStopSprint:
		c.ac.Sprinting.Store(false)
	}
}

// onSetHeldItem handles Set Held Item: the new hotbar slot (0..8), kept for
// Use Item dispatch.
func (c *ClientConnection) onSetHeldItem(slot int16) {
	// Anything else would address main-inventory slots as "held"
	// (inventory.HotbarStart + slot): using a sword straight from the backpack.
	if !anticheat.ValidHotbarSlot(int(slot)) {
		c.flag("badpackets.helditem", fmt.Sprintf("held slot %d", slot))
		return
	}
	c.heldSlot.Store(int32(slot))
	c.cancelUse()
	c.equipmentChanged()
}

// onChat handles Chat Message: validation, the auth gate, mutes, the chat
// moderator and the game's OnChat, then the instance broadcast.
func (c *ClientConnection) onChat(message string) {
	if !c.acceptChat(message) {
		return
	}
	// Auth gate: until /login or /register succeeds, chat is silent
	// for everyone else. Player gets a reminder line.
	if !gateAuth(c, "Please /login or /register before chatting.") {
		return
	}
	// Mute check happens before the moderator + per-instance OnChat
	// hook so games and bots can't accidentally bypass it. The muted
	// player gets a one-line reminder; nobody else sees anything.
	if until, muted := c.server.Mutes.MutedUntil(c.playerName); muted {
		_ = c.sendSystemMessage(fmt.Sprintf("You are muted until %s",
			until.Format("2006-01-02 15:04:05")))
		return
	}
	// Custom chat bot (e.g. anti-spam / anti-flame). Returns the text
	// to broadcast and an allow flag; may also install a mute.
	if rewritten, allow := c.server.applyChatModerator(c, message); !allow {
		return
	} else {
		message = rewritten
	}
	slog.Info("chat", "player", c.playerName, "msg", message)
	rewrite, allow := c.instance.filterChat(c, message)
	if !allow {
		return
	}
	c.instance.BroadcastChat(c.playerName, rewrite)
}

// onClickContainer handles Click Container (head decoded; the changed slots
// and carried item are still in packet — they only matter for window 0 and
// chests). Menus use the slot plus button/mode (shift-click, right click).
func (c *ClientConnection) onClickContainer(k packets.Click, packet *bytes.Buffer) {
	click := menuClick{button: k.Button, mode: k.Mode}
	slot := k.Slot
	if k.Window == 0 {
		// The player's own inventory screen: simulated on the model
		// (click.go); the client's predicted result is only compared.
		claim, err := inventory.ReadClaim(packet)
		if err != nil {
			c.flag("badpackets.click", err.Error())
			_ = c.sendInventoryContents()
			return
		}
		if c.gamemode() == player.Spectator {
			_ = c.sendInventoryContents() // spectators only look
			return
		}
		c.clickInventory(int(slot), int(k.Button), k.Mode, claim)
		return
	}
	if m := c.menu.Load(); m != nil && k.Window == menuWindowID {
		switch {
		case m.kind == "chest":
			// Simulated against the chest + inventory (click.go).
			claim, err := inventory.ReadClaim(packet)
			if err != nil {
				c.flag("badpackets.click", err.Error())
				_ = c.sendChestInventory(m.chest.Contents())
				return
			}
			if c.gamemode() == player.Spectator {
				_ = c.sendChestInventory(m.chest.Contents())
				return
			}
			c.clickChest(m.chest, int(slot), int(k.Button), k.Mode, claim)
			if e, ok := m.chest.(*enderchest.Chest); ok && c.server != nil {
				c.server.saveEnderChest(e)
			}
		case m.kind == menuKindPlugin:
			// Plugin GUI (shops): dispatch, then re-send the whole window
			// so the client's ghost pickup / local inventory moves are
			// undone and the menu stays open for the next click.
			if entry, ok := m.entries[slot]; ok && m.onClick != nil {
				m.onClick(c, entry, click)
			}
			if c.menu.Load() == m {
				_ = c.sendChestContents(m.rows, m.entries)
			}
		default:
			// Navigation menu: dispatch the clicked icon. The rest of the
			// packet (changed slots, carried item) is discarded.
			if entry, ok := m.entries[slot]; ok && m.onClick != nil {
				m.onClick(c, entry, click)
			}
		}
	}
}

// onCloseContainer handles Close Container: clear our menu state if it
// matches AND re-send the whole player inventory from the model — a stray
// ghost-click on a menu icon can leave the client's cursor/slots out of
// step, and the full Set Container Content puts every slot back.
func (c *ClientConnection) onCloseContainer(winID byte) {
	// Closing any window (the inventory screen is window 0) with a stack
	// still on the cursor drops it, like vanilla; the inventory screen
	// also hands back its 2×2 crafting grid.
	c.drag.Reset()
	c.dropCursor()
	if winID == 0 {
		c.returnCraftGrid()
	}
	if winID == menuWindowID {
		c.menu.Store(nil)
		_ = c.sendInventoryContents()
	}
}
