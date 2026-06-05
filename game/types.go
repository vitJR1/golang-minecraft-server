// Package game is the plugin-API surface for mini-game implementations.
//
// A mini-game lives in its own Go package and registers itself in init()
// via Register. The server's matchmaker (or admin commands) then create
// Instances of that game, each with its own world cloned from the
// definition's template, and each driven by a fresh Logic value.
//
// game/ deliberately does NOT import server/. It exposes Instance and
// PlayerHandle as interfaces; server/ provides adapter types that
// implement them. This keeps the plugin surface small (callers can only
// see what the interfaces declare) and prevents plugins from reaching
// into wire-level concerns like raw connections, encryption, or packet
// framing.
package game

import (
	"minecraft-server/player"
	"minecraft-server/world"
)

// Definition is the static metadata for a mini-game: how it's matched,
// what world it uses, and how to create a fresh Logic for each round.
// Registered once via Register(); the same Definition is reused for every
// instance of the game.
type Definition struct {
	// ID is the stable, lowercase identifier used in commands and matchmaker
	// queues ("skywars", "bedwars-doubles").
	ID string

	// Name is the human-readable name shown in UIs.
	Name string

	// MinPlayers and MaxPlayers bound the lobby size. Matchmaker waits for
	// at least MinPlayers; refuses past MaxPlayers.
	MinPlayers int
	MaxPlayers int

	// Template is the world snapshot every round starts from. Each
	// Instance gets its own MemoryWorld via Template.Instantiate().
	Template *world.Template

	// New constructs a fresh Logic value for one round. Called once per
	// Instance; the returned Logic owns that round's mutable state.
	New func() Logic
}

// Logic is the per-instance behavior a mini-game implements. Every method
// runs on the server's hot path; keep them fast. Long-running work goes
// in a goroutine the implementation spawns itself.
//
// Most methods have no return value; OnBlockBreak/OnBlockPlace return
// false to veto the change (client rolls back), and OnChat may rewrite
// the message or drop it entirely.
//
// Embed NoopLogic to satisfy the interface with default behavior, then
// override only the hooks you care about.
type Logic interface {
	// OnInstanceStart fires once after the instance is created and ready
	// to receive players. World is already populated from the template.
	OnInstanceStart(*Ctx)

	// OnInstanceEnd fires once after the last player leaves OR
	// Ctx.EndGame() is called. World may still be inspected here; after
	// the hook returns the instance is destroyed.
	OnInstanceEnd(*Ctx)

	// OnPlayerJoin fires after the player is fully visible in the
	// instance (tab list + spawned entity for others).
	OnPlayerJoin(*Ctx, PlayerHandle)

	// OnPlayerLeave fires before the player is removed from the
	// instance's player list. The player is still queryable via Ctx
	// during this hook.
	OnPlayerLeave(*Ctx, PlayerHandle)

	// OnTick fires once per game tick (20 Hz). Use the tick counter to
	// schedule periodic events ("if tick % 20 == 0", "if tick == start + 600").
	OnTick(*Ctx, uint64)

	// OnBlockBreak returns true to allow the break, false to veto.
	// Vetoed breaks send a corrective Block Update so the client rolls
	// back its prediction.
	OnBlockBreak(*Ctx, PlayerHandle, world.Position) bool

	// OnBlockPlace returns true to allow, false to veto.
	OnBlockPlace(*Ctx, PlayerHandle, world.Position, world.Block) bool

	// OnChat may rewrite the outgoing text and/or veto delivery. Return
	// (msg, true) for unchanged + allow, ("", false) for drop.
	OnChat(*Ctx, PlayerHandle, string) (string, bool)

	// OnPlayerAttack fires when one player sends an "attack" Interact
	// targeted at another player in the same instance. Return false to veto
	// the hit — no damage, knockback, or death results (use for teams,
	// spawn protection, spectators). Return true to let the core 1.9 combat
	// system resolve the hit (when the instance has PvP enabled).
	OnPlayerAttack(ctx *Ctx, attacker, target PlayerHandle) bool

	// OnPlayerDeath fires when the combat system kills a player. killer is
	// the attacker, or nil for an environmental/unknown death. Award kills,
	// drops, or scoreboards here. The respawn (death screen or instant, per
	// the instance's combat config) is handled by the server around this
	// call; a game may additionally Teleport the victim to a custom spawn.
	OnPlayerDeath(ctx *Ctx, victim, killer PlayerHandle)
}

// Ctx carries the per-instance handles the Logic needs. Constructed by
// the server and passed into every hook call.
type Ctx struct {
	// InstanceID is the same as Instance.ID() — duplicated for cheap
	// access without a method call.
	InstanceID string

	// Instance gives the Logic access to its own world and players.
	Instance Instance
}

// Instance is the slice of *server.Instance the plugin is allowed to
// touch. Server provides an adapter that implements this interface.
type Instance interface {
	// ID returns the instance's identifier (matches Definition.ID +
	// a uniquifier for each round).
	ID() string

	// SetBlock changes a block in this instance's world. The change is
	// broadcast to every player in the instance.
	SetBlock(p world.Position, b world.Block)

	// GetBlock reads a block from this instance's world.
	GetBlock(p world.Position) world.Block

	// BroadcastChat sends a chat line to every player. An empty sender
	// renders as a server announcement (no angle brackets).
	BroadcastChat(sender, message string)

	// PlayerCount returns the number of players currently in the instance.
	PlayerCount() int

	// Players returns a snapshot of every player currently in the
	// instance. The returned slice is safe to iterate without holding any
	// lock; new joiners/leavers are not reflected after the snapshot.
	Players() []PlayerHandle

	// PlayerByName looks up a player in this instance only.
	PlayerByName(name string) (PlayerHandle, bool)

	// EndGame signals the server to tear down this instance. All players
	// are moved to the hub, then OnInstanceEnd fires, then the instance
	// is removed.
	EndGame()

	// SetPvP enables or disables the 1.9 combat system (damage, knockback,
	// death) for this instance. Instances default to PvP enabled; the hub
	// defaults to disabled. Call from OnInstanceStart.
	SetPvP(enabled bool)

	// SetInstantRespawn controls death behavior. When true (suited to
	// arenas), a killed player is immediately healed and respawned at the
	// instance spawn with no death screen. When false (the default), the
	// vanilla death screen is shown and the player respawns on click.
	SetInstantRespawn(enabled bool)
}

// PlayerHandle is the safe wrapper around a connected player. Plugins
// only see this — never *server.ClientConnection or raw network state.
type PlayerHandle interface {
	// Name is the player's chosen username (immutable for the session).
	Name() string

	// EntityID is the unique-per-server entity ID for this player.
	EntityID() int32

	// Pose returns a consistent snapshot of position, rotation, and
	// gamemode at the moment of the call.
	Pose() player.Snapshot

	// Teleport moves the player to (x, y, z) within their current
	// instance, sending Synchronize Player Position to the client and
	// broadcasting Teleport Entity to the rest.
	Teleport(x, y, z float64)

	// SendMessage delivers a system chat line just to this player. The
	// text is wrapped in a JSON chat component server-side.
	SendMessage(text string)

	// SetGamemode switches the player to gamemode g and tells the client.
	SetGamemode(g player.Gamemode)

	// Kick closes the player's connection. The reason is logged but not
	// (yet) sent as a Disconnect message — that needs the Play Disconnect
	// packet, which we haven't wired up.
	Kick(reason string)
}
