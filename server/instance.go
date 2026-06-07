package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"minecraft-server/cfg"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"sync"
	"sync/atomic"
	"time"
)

// TickRate is the per-instance tick frequency in Hz. Matches vanilla.
const TickRate = 20

// tickInterval is the wall-clock duration between ticks.
var tickInterval = time.Second / TickRate

// TickHandler fires once per tick. tick is the monotonically-increasing
// counter since the instance started. Handlers should be fast — if any
// handler takes longer than tickInterval, the next tick is dropped (the
// ticker doesn't queue), so a slow handler stretches game time visibly.
type TickHandler func(tick uint64)

// CombatConfig tunes the 1.9-style PvP system per instance. Zero value is
// not usable — construct via DefaultCombatConfig and tweak. All damage is
// in half-heart units (20 = full health). These are the numeric knobs only;
// the live on/off toggles (PvP enabled, instant respawn) live on Instance as
// atomics so they can be flipped at runtime (e.g. via /instance set pvp).
type CombatConfig struct {
	// Version is the combat model: PvP18 (no cooldown) or PvP19 (charge
	// scaling). Defaults from cfg.PvPVersion (PVP_VERSION env var).
	Version PvPVersion

	// BaseDamage is the weapon's raw hit damage at full charge, before the
	// cooldown scaling and crit multiplier. 6 ≈ a vanilla iron sword.
	BaseDamage float32

	// AttackSpeed is attacks-per-second (vanilla "Attack Speed" attribute):
	// fist 4.0, sword 1.6, axe 1.0. Drives the cooldown charge window —
	// cooldown is 1/AttackSpeed seconds.
	AttackSpeed float32

	// Knockback is the horizontal launch strength of a normal hit, in
	// blocks/tick. SprintKnockback is the extra added when the attacker is
	// sprinting (the 1.9 "w-tap"). VerticalKnockback is the upward launch.
	Knockback         float32
	SprintKnockback   float32
	VerticalKnockback float32

	// CritMultiplier scales damage on a critical hit (attacker falling, at
	// full charge). Vanilla is 1.5.
	CritMultiplier float32

	// InvulnTicks is the per-target i-frame window (vanilla: 10 ticks).
	InvulnTicks uint64

	// RegenIntervalTicks is how often a below-max, alive player heals one
	// half-heart. 0 disables natural regen.
	RegenIntervalTicks uint64
}

// DefaultCombatConfig returns the out-of-the-box PvP tuning: a sword-like
// weapon with the full 1.9 cooldown/crit/knockback model and slow natural
// regen. Enabled by default — disable it on safe instances (e.g. the hub).
func DefaultCombatConfig() CombatConfig {
	return CombatConfig{
		Version:            pvpVersionFromCfg(),
		BaseDamage:         6,
		AttackSpeed:        1.6,
		Knockback:          0.4,
		SprintKnockback:    0.4,
		VerticalKnockback:  0.4,
		CritMultiplier:     1.5,
		InvulnTicks:        10,
		RegenIntervalTicks: 40, // ~2s per half-heart
	}
}

// pvpVersionFromCfg maps the cfg.PvPVersion int (set from PVP_VERSION) to a
// PvPVersion, falling back to 1.9 for any unrecognized value.
func pvpVersionFromCfg() PvPVersion {
	if cfg.PvPVersion == int(PvP18) {
		return PvP18
	}
	return PvP19
}

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

	// Combat holds the numeric PvP tuning for this instance (damage, speed,
	// knockback). Set at construction (DefaultCombatConfig) and safe to
	// tweak before traffic arrives; the live on/off toggles below are
	// separate atomics so they can flip mid-fight without a data race.
	Combat CombatConfig

	// combatEnabled gates the whole PvP system; instantRespawn picks the
	// death behavior (instant heal+teleport vs death screen). Atomic so
	// /instance set and game logic can toggle them while players fight.
	combatEnabled  atomic.Bool
	instantRespawn atomic.Bool

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
	// position (27 slots each). Populated lazily on first interaction;
	// guarded by chestsMu (multiple players may open chests concurrently).
	chestsMu sync.Mutex
	chests   map[world.Position]*chestInventory

	// projectiles are in-flight thrown items (egg/snowball/ender pearl),
	// simulated on the tick loop. Guarded by projMu.
	projMu      sync.Mutex
	projectiles []*projectile

	// joinMu serializes registration + visibility announcements per
	// instance, so one player's join can't observe another mid-join inside
	// the same instance.
	joinMu sync.Mutex

	// Tick loop state.
	tickCount    atomic.Uint64
	tickMu       sync.RWMutex
	tickHandlers []TickHandler
	stopTick     chan struct{} // closed by Stop()
	stopOnce     sync.Once

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
	// (when PvP is enabled — see combatEnabled).
	OnPlayerAttack func(attacker, target *ClientConnection) bool

	// OnPlayerDeath fires when the combat system kills a player. killer is
	// the attacker, or nil for an environmental death. Called before the
	// respawn (death screen or instant) is finalized.
	OnPlayerDeath func(victim, killer *ClientConnection)

	// OnStop fires once when the instance is being torn down (via
	// Server.RemoveInstance or Instance.Stop). Use for game cleanup;
	// the tick loop is still running when this fires.
	OnStop func()
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
		Combat:     DefaultCombatConfig(),
		SpawnPoint: Spawn{X: 0.5, Y: 67, Z: 0.5},
		stopTick:   make(chan struct{}),
	}
	i.combatEnabled.Store(true) // instances default to PvP on (hub turns it off)
	i.loadWorldEntities()
	i.OnTick(i.combatTick)
	i.OnTick(i.projectileTick)
	go i.tickLoop()
	return i
}

// PvPEnabled reports whether the 1.9 combat system is active on this
// instance. SetPvP toggles it (thread-safe, may be called mid-fight).
func (i *Instance) PvPEnabled() bool    { return i.combatEnabled.Load() }
func (i *Instance) SetPvP(enabled bool) { i.combatEnabled.Store(enabled) }

// InstantRespawn reports the death behavior: true = heal+teleport on the
// spot, false = vanilla death screen. SetInstantRespawn toggles it.
func (i *Instance) InstantRespawn() bool           { return i.instantRespawn.Load() }
func (i *Instance) SetInstantRespawn(enabled bool) { i.instantRespawn.Store(enabled) }

// OnTick registers a callback fired once per tick (20 Hz). Concurrent-safe.
// There is no Unsubscribe yet — destroy the whole instance via Stop when
// you want everything to go away.
func (i *Instance) OnTick(h TickHandler) {
	i.tickMu.Lock()
	i.tickHandlers = append(i.tickHandlers, h)
	i.tickMu.Unlock()
}

// Tick returns the current tick count (0 immediately after NewInstance).
func (i *Instance) Tick() uint64 {
	return i.tickCount.Load()
}

// Stop halts the tick loop and fires OnStop (under panic recovery). Safe
// to call multiple times. Does not affect connected players or pending
// broadcasts — those continue running on their own goroutines.
func (i *Instance) Stop() {
	i.stopOnce.Do(func() {
		if i.OnStop != nil {
			safeHook(i, "OnStop", i.OnStop)
		}
		close(i.stopTick)
	})
}

func (i *Instance) tickLoop() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-i.stopTick:
			return
		case <-ticker.C:
			i.runTick()
		}
	}
}

func (i *Instance) runTick() {
	tick := i.tickCount.Add(1)

	// Snapshot the handler list so a handler can OnTick a new subscriber
	// without deadlocking, and so we don't hold the lock during user code.
	i.tickMu.RLock()
	handlers := make([]TickHandler, len(i.tickHandlers))
	copy(handlers, i.tickHandlers)
	i.tickMu.RUnlock()

	for _, h := range handlers {
		// Isolate panics: one buggy game shouldn't freeze the instance.
		safeTick(i, h, tick)
	}
}

func safeTick(i *Instance, h TickHandler, tick uint64) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("tick handler panic",
				"instance", i.ID, "tick", tick, "panic", fmt.Sprint(r))
		}
	}()
	h(tick)
}

// JoinAndAnnounce registers c in this instance's player list and sends the
// visibility packets that make c and the existing players mutually
// visible. Run AFTER c.player and c.instance are set. Fires OnPlayerJoin
// once everything is wired up so game logic sees a fully-joined player.
func (i *Instance) JoinAndAnnounce(c *ClientConnection) {
	i.joinMu.Lock()

	i.Players.Add(c)

	others := i.Players.snapshot()

	// 1. Newcomer gets the full tab list (everyone in THIS instance).
	if payload := playerInfoAddPayload(others); payload != nil {
		_ = c.safeWrite(CbPlayPlayerInfoUpdate, payload)
	}

	// 2. Newcomer spawns the visible entity for every other player here.
	for _, other := range others {
		if other == c {
			continue
		}
		_ = c.safeWrite(CbPlaySpawnPlayer, spawnPlayerPayload(other.player))
	}

	// 3. Everyone else in the instance learns about the newcomer.
	addNewcomer := playerInfoAddPayload([]*ClientConnection{c})
	spawnNewcomer := spawnPlayerPayload(c.player)
	i.Players.Broadcast(CbPlayPlayerInfoUpdate, addNewcomer, c.player.EntityID)
	i.Players.Broadcast(CbPlaySpawnPlayer, spawnNewcomer, c.player.EntityID)

	hook := i.OnPlayerJoin
	i.joinMu.Unlock()

	// Hook fires outside the lock so game logic can call back into the
	// instance without deadlocking.
	if hook != nil {
		safeHook(i, "OnPlayerJoin", func() { hook(c) })
	}
}

// LeaveAndAnnounce removes the player from the instance and tells the
// remaining players to despawn them. Fires OnPlayerLeave BEFORE the player
// is dropped so game logic can still see them in the list. No-op when
// c.player is nil (early disconnect before login completed).
func (i *Instance) LeaveAndAnnounce(c *ClientConnection) {
	if c.player == nil {
		return
	}

	// Hook outside joinMu so game cleanup can call into the instance.
	if hook := i.OnPlayerLeave; hook != nil {
		safeHook(i, "OnPlayerLeave", func() { hook(c) })
	}

	i.joinMu.Lock()
	defer i.joinMu.Unlock()

	i.Players.Broadcast(
		CbPlayRemoveEntities,
		removeEntitiesPayload([]int32{c.player.EntityID}),
		c.player.EntityID,
	)
	i.Players.Broadcast(
		CbPlayPlayerInfoRemove,
		playerInfoRemovePayload([][16]byte{c.player.UUID}),
		c.player.EntityID,
	)
	i.Players.Remove(c.player.EntityID)
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

	var buf bytes.Buffer
	buf.Write(protocol.WritePosition(p.X, p.Y, p.Z))
	protocol.WriteVarInt32ToBuffer(&buf, b.StateID)
	i.Players.Broadcast(CbPlayBlockUpdate, buf.Bytes(), -1)
}

// BroadcastChat sends a chat line to every player in this instance. Format
// is "<sender> message"; an empty sender renders as a plain server line.
func (i *Instance) BroadcastChat(sender, message string) {
	var line string
	if sender == "" {
		line = message
	} else {
		line = fmt.Sprintf("<%s> %s", sender, message)
	}
	i.Players.Broadcast(CbPlaySystemChat, buildSystemChatPayload(line), -1)
}

// buildSystemChatPayload assembles a System Chat (Cb 0x64) body: JSON chat
// component string + overlay bool. encoding/json handles escaping so we
// can pass arbitrary text without breaking the wire format.
func buildSystemChatPayload(text string) []byte {
	encoded, _ := json.Marshal(map[string]string{"text": text})
	return append(protocol.WriteString(string(encoded)), 0)
}
