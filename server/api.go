package server

import (
	"time"

	"minecraft-server/player"
	"minecraft-server/server/botai"
	"minecraft-server/server/effect"
	"minecraft-server/server/lobby"
)

// api.go: what the feature packages built over the server (server/commands,
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
func (s *Server) ToHub(c *ClientConnection) (ok bool, err error) {
	// Choosing the hub means "no game right now", wherever the player is.
	s.Matchmaker.Dequeue(c)
	if c.instance == s.Hub {
		return false, nil
	}
	return true, s.MovePlayer(c, s.Hub, 0.5, 67, 0.5)
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
	return "", s.MovePlayer(c, target, 0.5, 67, 0.5)
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
