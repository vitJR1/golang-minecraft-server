package server

import (
	"bytes"
	"fmt"
	"log/slog"
	"minecraft-server/game"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/bots/botai"
	"minecraft-server/server/gameplay/chest"
	"minecraft-server/server/gameplay/combat"
	"minecraft-server/server/gameplay/fluid"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/server/gameplay/itementity"
	"minecraft-server/server/hud/chat"
	"minecraft-server/server/hud/visibility"
	"minecraft-server/server/instances/rules"
	"minecraft-server/server/instances/tickloop"
	"minecraft-server/world"
	"sync"
	"sync/atomic"
)

// Spawn is a respawn/teleport target. Defaults to the world origin column.
type Spawn struct {
	X, Y, Z float64
}

// Instance is one game-world "room": its own block storage, its own
// player list, its own tick loop, its own broadcast scope. The Hub
// instance is just an instance like any other. Cross-instance teleport
// (Server.MovePlayer) is a later step.
//
// All visibility-affecting operations live on Instance: chat broadcasts,
// block updates, player join/leave announcements. Server stays the
// coordinator (entity-ID allocator, ops, listener of TCP).
//
// Event hooks (OnPlayerJoin/Leave/etc.) are the integration point for
// game logic. Set them at instance construction; the core server calls
// them at well-defined points and respects their return values (veto for
// block events, rewrite/veto for chat). nil hook = default (allow + no-op).
type Instance struct {
	ID      string
	Server  *Server
	World   world.World
	Players *PlayerList

	// GameID is the game this instance runs: the Definition ID for a
	// matchmaker round ("bedwars", "ffa"), the arena kind for a created
	// arena ("bedwars-1x1"); "" for the hub, lobbies and plain instances.
	// /leave uses it to find the mode's lobby. Set before players join.
	GameID string

	// ephemeralState carries the janitor's collectability flag + empty
	// timestamp — see janitor.go.
	ephemeralState

	// Combat holds the numeric PvP tuning for this instance (damage, speed,
	// knockback). Set at construction (combat.DefaultConfig) and safe to
	// tweak before traffic arrives; the live on/off toggles are the
	// embedded rules.Switches (PvPEnabled/SetPvP, InstantRespawn, …), atomics
	// so they can flip mid-fight without a data race.
	Combat combat.Config
	rules.Switches

	// SpawnPoint is where players respawn after death (and the world spawn
	// for this instance). Defaults to the origin column.
	SpawnPoint Spawn

	// worldEntities are the non-player entities (item frames) in this
	// instance's world. Baked ones get a stable server entity ID at
	// construction; players can add more at runtime (AddWorldEntity). Each is
	// sent to clients on join. Guarded by entitiesMu (runtime placement races
	// concurrent joins).
	entitiesMu    sync.Mutex
	worldEntities []instanceEntity

	// chests holds the stored contents of opened chests, keyed by block
	// position (27 slots each), created on first interaction.
	chests chest.Chests

	// projectiles are in-flight thrown items (egg/snowball/ender pearl),
	// simulated on the tick loop. Guarded by projMu.
	projMu      sync.Mutex
	projectiles []*projectileEntity

	// items are dropped-item entities (generator output) lying in this
	// instance, simulated on the tick loop (fall, pickup, despawn). Guarded by
	// itemsMu.
	itemsMu sync.Mutex
	items   []*itementity.Entity

	// tnts are primed TNT entities counting down to their explosion
	// (tnt.go). Guarded by tntMu.
	tntMu sync.Mutex
	tnts  []*primedTNT

	// holograms are floating-text armor stands (game labels / timers).
	// Guarded by holoMu.
	holoMu    sync.Mutex
	holograms []*floatingText

	// fluids are the block positions whose fluid state must be re-evaluated
	// on their next due tick (fluid.go).
	fluids fluid.Queue

	// joinMu serializes registration + visibility announcements per
	// instance, so one player's join can't observe another mid-join inside
	// the same instance.
	joinMu sync.Mutex
	// removed is set by RemoveInstance (under joinMu): no join after it.
	removed bool

	// chunks is the baked world streamed to joiners (chunk_cache.go).
	chunks chunkCache

	// streams buffers block updates for players MovePlayer is streaming
	// the world to (join_stream.go).
	streams blockStreams

	// ticks is the instance's clock (server/instances/tickloop): handlers, counter,
	// /stats timings.
	ticks tickloop.Loop

	// Event hooks. Set at construction, read from handler goroutines.
	// Don't reassign after the instance starts taking traffic — there's no
	// synchronization on these fields.
	OnPlayerJoin  func(c *ClientConnection)
	OnPlayerLeave func(c *ClientConnection)
	// OnBlockBreak / OnBlockPlace return true to allow the action. False
	// causes the server to send a corrective Block Update with the prior
	// state so the client rolls back its prediction.
	OnBlockBreak func(c *ClientConnection, pos world.Position) bool
	OnBlockPlace func(c *ClientConnection, pos world.Position, block world.Block) bool
	// OnChat may rewrite the message text and/or veto delivery. The first
	// return is the (possibly modified) text to broadcast; the second is
	// false to drop the message entirely.
	OnChat func(c *ClientConnection, msg string) (rewrite string, allow bool)

	// OnPlayerAttack fires when attacker sends SbPlayInteract type=attack
	// targeting another player in the same instance. Returning false vetoes
	// the hit; true lets the core combat system resolve damage/knockback
	// (when PvP is enabled — see PvPEnabled).
	OnPlayerAttack func(attacker, target *ClientConnection) bool

	// OnPlayerDeath fires when the combat system kills a player. killer is
	// the attacker, or nil for an environmental death. Called before the
	// respawn (death screen or instant) is finalized.
	OnPlayerDeath func(victim, killer *ClientConnection)

	// OnExplosionBreak, when set, is asked before an explosion destroys a
	// block (game.ExplosionBreakVeto); false keeps it. c is the igniter or
	// nil.
	OnExplosionBreak func(c *ClientConnection, pos world.Position) bool

	// OnForceStart, when set, starts the instance's round now
	// (game.ForceStarter, /forcestart).
	OnForceStart func() error

	// botGuide is the game's game.BotGuide, when it supports bots (/bot).
	botGuide game.BotGuide
	// botTeams are the bot teams' shared brains, by team name
	// (bot_team.go). Touched only on the tick goroutine.
	botTeams botai.Teams

	// tags are the players' name-tag groups by player name (name_tags.go),
	// guarded by tagsMu.
	tagsMu sync.Mutex
	tags   map[string]game.NameTag

	// fixedTime is the frozen time of day + 1 (0 = the clock runs);
	// day_time.go.
	fixedTime atomic.Int64

	// OnRewritePlace, when set, may swap the block a player is about to
	// place (game.PlacementRewriter). Runs before OnBlockPlace.
	OnRewritePlace func(c *ClientConnection, pos world.Position, blk world.Block) world.Block

	// OnBlockInteract fires for a left/right click on a block before the
	// core treats it as a dig / placement / container open. false = consume
	// (see game.BlockInteraction).
	OnBlockInteract func(c *ClientConnection, click game.BlockInteraction) bool

	// OnEntityInteract fires for a right-click on a world entity. false =
	// consume (the core's item-frame default doesn't run).
	OnEntityInteract func(c *ClientConnection, ei game.EntityInteraction) bool

	// OnItemUse fires for a right-click in the air with the held item before
	// the core's item behaviours. false = consume.
	OnItemUse func(c *ClientConnection, use game.ItemUse) bool

	// OnItemConsume fires when eating/drinking finishes. true = the game
	// handled the effect; the core then only consumes the item.
	OnItemConsume func(c *ClientConnection, st inventory.Stack) bool

	// OnStop fires once when the instance is being torn down (via
	// Server.RemoveInstance or Instance.Stop). Use for game cleanup;
	// the tick loop is still running when this fires.
	OnStop func()

	// ctx is the game.Ctx handed to plugin listeners and commands for this
	// instance; listeners is the snapshot of game.Listeners() taken at
	// construction. Both are set once in NewInstance (see plugins.go).
	ctx       *game.Ctx
	listeners []game.Listener
}

// NewInstance creates an instance and starts its tick loop. Caller
// registers it on the server (currently just by assigning to Server.Hub
// or by future matchmaker logic).
func NewInstance(id string, srv *Server, w world.World) *Instance {
	i := &Instance{
		ID:         id,
		Server:     srv,
		World:      w,
		Players:    NewPlayerList(),
		Combat:     combat.DefaultConfig(),
		SpawnPoint: Spawn{X: 0.5, Y: 67, Z: 0.5},
	}
	i.ticks.Label = id
	i.SetPvP(true) // instances default to PvP on (hub turns it off)
	i.loadWorldEntities()
	i.OnTick(i.combatTick)
	i.OnTick(i.projectileTick)
	i.OnTick(i.fluidTick)
	i.OnTick(i.burnTick)
	i.OnTick(i.effectsTick)
	i.OnTick(i.consumeTick)
	i.OnTick(i.itemTick)
	i.OnTick(i.tntTick)
	i.OnTick(i.anticheatTick)
	i.OnTick(i.botTick)
	i.initListeners()
	go i.ticks.Run()
	return i
}

// OnTick registers a callback fired once per tick (20 Hz). Concurrent-safe.
// There is no Unsubscribe yet — destroy the whole instance via Stop when
// you want everything to go away.
func (i *Instance) OnTick(h tickloop.Handler) { i.ticks.Handle(h) }

// Tick returns the current tick count (0 immediately after NewInstance).
func (i *Instance) Tick() uint64 { return i.ticks.Count() }

// Stop halts the tick loop and fires listeners' OnInstanceEnd + OnStop
// (under panic recovery). Safe to call multiple times. Does not affect
// connected players or pending broadcasts — those continue running on their
// own goroutines.
func (i *Instance) Stop() { i.ticks.Stop(i.fireStop) }

// JoinAndAnnounce registers c in this instance's player list and sends the
// visibility packets that make c and the existing players mutually
// visible. Run AFTER c.player and c.instance are set. Fires OnPlayerJoin
// once everything is wired up so game logic sees a fully-joined player.
//
// Returns false, and does nothing, once RemoveInstance has taken the
// instance down.
func (i *Instance) JoinAndAnnounce(c *ClientConnection) bool {
	i.joinMu.Lock()
	if i.removed {
		i.joinMu.Unlock()
		return false
	}

	i.Players.Add(c)

	others := i.Players.snapshot()

	// 1. Newcomer gets the full tab list (everyone in THIS instance).
	if payload := visibility.InfoAddPayload(tabEntries(others)); payload != nil {
		_ = c.safeWrite(packetid.CbPlayPlayerInfoUpdate, payload)
	}

	// 2. Newcomer spawns the visible entity for every other player here.
	for _, other := range others {
		if other == c {
			continue
		}
		_ = c.safeWrite(packetid.CbPlaySpawnPlayer, visibility.SpawnPlayerPayload(other.player.Snapshot()))
	}

	c.sendOthersEquipment()

	// 3. Everyone else in the instance learns about the newcomer.
	addNewcomer := visibility.InfoAddPayload(tabEntries([]*ClientConnection{c}))
	spawnNewcomer := visibility.SpawnPlayerPayload(c.player.Snapshot())
	i.Players.Broadcast(packetid.CbPlayPlayerInfoUpdate, addNewcomer, c.player.EntityID)
	i.Players.Broadcast(packetid.CbPlaySpawnPlayer, spawnNewcomer, c.player.EntityID)
	i.Players.Broadcast(packetid.CbPlaySetEquipment, c.equipmentPayload(), c.player.EntityID)

	i.joinMu.Unlock()

	// The instance's name-tag groups replace the previous instance's; its
	// frozen time of day replaces the fresh level's clock.
	c.syncNameTags(i)
	c.sendDayTime()

	// Hooks fire outside the lock so game logic can call back into the
	// instance without deadlocking.
	i.fireJoin(c)
	return true
}

// isRemoved reports whether RemoveInstance has taken the instance down.
func (i *Instance) isRemoved() bool {
	i.joinMu.Lock()
	defer i.joinMu.Unlock()
	return i.removed
}

// LeaveAndAnnounce removes the player from the instance and tells the
// remaining players to despawn them. Fires OnPlayerLeave BEFORE the player
// is dropped so game logic can still see them in the list. No-op when
// c.player is nil (early disconnect before login completed) or c isn't in
// the instance (a join RemoveInstance refused). Returns the UUIDs the
// instance had right before c left, c's own included — what c's tab list
// must drop — taken under joinMu so a concurrent join can't be missed.
func (i *Instance) LeaveAndAnnounce(c *ClientConnection) [][16]byte {
	if c.player == nil {
		return nil
	}
	if m, ok := i.Players.Get(c.player.EntityID); !ok || m != c {
		return nil
	}

	// Hooks outside joinMu so game cleanup can call into the instance.
	i.fireLeave(c)

	i.joinMu.Lock()
	defer i.joinMu.Unlock()
	uuids := i.Players.UUIDs()

	i.Players.Broadcast(
		packetid.CbPlayRemoveEntities,
		visibility.RemoveEntitiesPayload([]int32{c.player.EntityID}),
		c.player.EntityID,
	)
	i.Players.Broadcast(
		packetid.CbPlayPlayerInfoRemove,
		visibility.InfoRemovePayload([][16]byte{c.player.UUID}),
		c.player.EntityID,
	)
	i.Players.Remove(c.player.EntityID)
	i.tagsMu.Lock()
	_, tagged := i.tags[c.playerName]
	delete(i.tags, c.playerName)
	i.tagsMu.Unlock()
	if tagged {
		i.Players.Range(func(o *ClientConnection) { o.syncNameTags(i) })
	}
	return uuids
}

// safeHook runs a hook function with panic recovery and a labelled log
// line, so a buggy game can't kill the calling goroutine.
func safeHook(i *Instance, name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("hook panic",
				"instance", i.ID, "hook", name, "panic", fmt.Sprint(r))
		}
	}()
	fn()
}

// SetBlock updates this instance's world and broadcasts a Block Update to
// every player here. Players in other instances see nothing.
func (i *Instance) SetBlock(p world.Position, b world.Block) {
	i.World.SetBlock(p, b)
	i.scheduleFluid(p)

	var buf bytes.Buffer
	buf.Write(protocol.WritePosition(p.X, p.Y, p.Z))
	protocol.WriteVarInt32ToBuffer(&buf, b.StateID)
	i.broadcastBlocks(packetid.CbPlayBlockUpdate, buf.Bytes())
}

// BroadcastChat sends a chat line to every player in this instance. Format
// is "<sender> message"; an empty sender renders as a plain server line.
func (i *Instance) BroadcastChat(sender, message string) {
	i.Players.Broadcast(packetid.CbPlaySystemChat, chat.SystemPayload(chat.Line(sender, message)), -1)
}
