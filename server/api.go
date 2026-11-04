package server

import (
	"log/slog"
	"time"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/bots/botai"
	"minecraft-server/server/gameplay/effect"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/server/gameplay/sound"
	"minecraft-server/server/instances/lobby"
)

// api.go: what the feature packages built over the server (server/features/commands,
// …) may do with a connection and the server. They import server; server
// never imports them — main blank-imports them so their init() registers
// them.

// RegisterCommand adds a built-in slash command (from a feature package's
// init()). Plugins use game.RegisterCommand instead.
func RegisterCommand(cmd *Command) { registerCommand(cmd) }

// Name is the player's chosen username (set at Login Start, immutable for
// the session).
func (c *ClientConnection) Name() string { return c.playerName }

// Player is the gameplay entity (nil before login completes).
func (c *ClientConnection) Player() *player.Player { return c.player }

// Instance is the instance the player is in (hub, an arena, a lobby); nil
// before login completes.
func (c *ClientConnection) Instance() *Instance { return c.instance }

// Server is the server the connection belongs to.
func (c *ClientConnection) Server() *Server { return c.server }

// SendMessage shows text in the player's chat as a system line.
func (c *ClientConnection) SendMessage(text string) error { return c.sendSystemMessage(text) }

// Kick disconnects the player with reason on their disconnect screen.
func (c *ClientConnection) Kick(reason string) {
	_ = c.sendPlayDisconnect(reason)
	go c.cleanup()
}

// ResendCommands re-sends the command tree (Declare Commands) — the client
// highlights and tab-completes from it, and op-only commands are left out
// for non-operators, so it is stale after /op or /deop.
func (c *ClientConnection) ResendCommands() error { return c.sendDeclareCommands() }

// SetGamemode changes the player's gamemode and tells the client.
func (c *ClientConnection) SetGamemode(mode player.Gamemode) {
	c.player.SetGamemode(mode)
	_ = c.sendGameModeChange(mode)
}

// Teleport moves the player within their instance: the authoritative
// position, the client's view (a tracked Synchronize Player Position) and
// the other players' (Teleport Entity).
func (c *ClientConnection) Teleport(x, y, z float64) {
	c.player.MoveTo(x, y, z, false)
	_ = c.sendSyncPlayerPosition(x, y, z)
	c.broadcastEntityTeleport()
}

// ApplyEffect grants e at level (1 = I) for d; ClearEffects ends them all.
func (c *ClientConnection) ApplyEffect(e effect.Effect, level int, d time.Duration) {
	c.applyEffect(e, level, d)
}
func (c *ClientConnection) ClearEffects() { c.clearEffects() }

// ToHub sends the player to the hub, out of any matchmaker queue. An error
// is reported for the caller to show; being in the hub already is not one
// (ok is false then).
// Join moves c into target, landing at target.Arrival.
func (s *Server) Join(c *ClientConnection, target *Instance) error {
	a := target.Arrival
	return s.MovePlayer(c, target, a.X, a.Y, a.Z)
}

func (s *Server) ToHub(c *ClientConnection) (ok bool, err error) {
	// Choosing the hub means "no game right now", wherever the player is.
	s.Matchmaker.Dequeue(c)
	if c.instance == s.Hub {
		return false, nil
	}
	return true, s.Join(c, s.Hub)
}

// LeaveGame sends the player out of the running game into its mode's
// lobby (the hub when the mode has none), the way the hub menu enters a
// lobby — spectator mode, kit and effects are shed on landing. reason is
// set (and nothing moves) when the player isn't in a game.
func (s *Server) LeaveGame(c *ClientConnection) (reason string, err error) {
	s.Matchmaker.Dequeue(c)
	inst := c.instance
	if inst == s.Hub {
		return "You're not in a game.", nil
	}
	if lobby.Is(inst.ID) {
		return "You're not in a game — /hub to go back to the hub.", nil
	}
	target := s.Hub
	if id, ok := lobby.ForGame(inst.GameID); ok {
		if dest := s.GetInstance(id); dest != nil {
			target = dest
		}
	}
	return "", s.Join(c, target)
}

// IsBot reports whether the connection is an AI player (/bot); Bot is its
// mind (nil for a real player).
func (c *ClientConnection) IsBot() bool     { return c.isBot() }
func (c *ClientConnection) Bot() *botai.Bot { return c.bot }

// CanFly reports whether /fly granted the player flight; SetFlight grants
// or takes it (Player Abilities, exempt from the flight check).
func (c *ClientConnection) CanFly() bool      { return c.canFly.Load() }
func (c *ClientConnection) SetFlight(on bool) { c.setFlight(on) }

// GiveItem puts n of itemID in the inventory (hotbar first); the count that
// didn't fit is returned. CountItem is how many the player holds.
func (c *ClientConnection) GiveItem(itemID int32, n int) int { return c.giveItem(itemID, n) }
func (c *ClientConnection) CountItem(itemID int32) int       { return c.countItem(itemID) }

// Kill deals a lethal environmental hit: through armour like anything
// else, but far beyond what armour absorbs, then the regular death path.
func (c *ClientConnection) Kill() {
	if c.player == nil || c.instance == nil {
		return
	}
	c.hurtEnvironment(player.MaxHealth*50, 0, c.instance.Tick())
}

// OpenMenu shows a rows×9 chest GUI with items; each click goes to onClick
// (the window is re-sent after it while the menu stays open). Plugins reach
// it through game.PlayerHandle.OpenMenu.
func (c *ClientConnection) OpenMenu(title string, rows int, items []game.MenuItem, onClick func(game.MenuClick)) {
	c.openPluginMenu(title, rows, items, onClick)
}

// CloseMenu forgets the open menu server-side (a move's Respawn closes the
// window on the client); MenuOpen reports whether one is open.
func (c *ClientConnection) CloseMenu()     { c.menu.Store(nil) }
func (c *ClientConnection) MenuOpen() bool { return c.menu.Load() != nil }

// GiveHotbarItem puts a named single item in window-0 slot (36..44 are the
// hotbar) and sends that one slot: the hub's navigator and selector.
func (c *ClientConnection) GiveHotbarItem(slot int16, itemID int32, name string) {
	st := inventory.Stack{ID: itemID, Count: 1, Name: name}
	c.inv.Set(slot, st)
	if err := c.sendSetSlot(0, slot, st); err != nil {
		slog.Warn("give hotbar item failed",
			"player", c.playerName, "item", name, "err", err)
	}
}

// Slot is window-0 slot i of the inventory model (empty out of range).
func (c *ClientConnection) Slot(i int16) inventory.Stack {
	if i < 0 || int(i) >= inventory.PlayerSlots {
		return inventory.Stack{}
	}
	return c.inv.Get(i)
}

// SetHealth sets the player's health (clamped by player.Player) and tells
// the client.
func (c *ClientConnection) SetHealth(h float32) {
	c.player.SetHealth(h)
	_ = c.sendSetHealth(c.player.Health())
}

// PlaySound plays a sound at the player's position, to that player only.
func (c *ClientConnection) PlaySound(name string, volume, pitch float32) {
	s := c.player.Snapshot()
	_ = c.safeWrite(packetid.CbPlaySoundEffect,
		sound.EffectPayload(name, sound.CategoryPlayer, s.X, s.Y, s.Z, volume, pitch))
}

// SetNameTag groups the player under tag in its instance (tab list + over
// the head; name_tags.go).
func (c *ClientConnection) SetNameTag(tag game.NameTag) {
	if inst := c.instance; inst != nil {
		inst.setNameTag(c.playerName, tag)
	}
}

// Respawn brings a dead (or alive) player back at (x, y, z) with full
// health: the game-driven counterpart of combat.go's respawn paths, for
// instances running SetCustomRespawn.
func (c *ClientConnection) Respawn(x, y, z float64) {
	c.player.Respawn()
	c.player.MoveTo(x, y, z, false)
	_ = c.sendSetHealth(player.MaxHealth)
	_ = c.sendSyncPlayerPosition(x, y, z)
	c.broadcastEntityTeleport()
	c.resendEffects()
	c.equipmentChanged()
}

// Die kills the player outright — no armour, no combat switch — and runs
// the death flow (games' Kill: the void). Kill is the armoured hit.
func (c *ClientConnection) Die() {
	if c.player == nil || c.player.IsDead() || c.instance == nil {
		return
	}
	c.player.ApplyDamage(player.MaxHealth*10, c.instance.Tick(), 0)
	c.die(nil)
}
