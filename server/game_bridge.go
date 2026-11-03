package server

import (
	"fmt"
	"minecraft-server/game"
	"minecraft-server/server/features/gamebridge"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/server/gameplay/sound"
	"minecraft-server/world"
	"time"
)

// instanceBridge implements game.Instance over *Instance. The bridge is
// stateless — every method delegates straight to the wrapped instance —
// so it's cheap to construct per call.
type instanceBridge struct {
	server *Server
	inst   *Instance
}

func (b instanceBridge) ID() string                                 { return b.inst.ID }
func (b instanceBridge) SetBlock(p world.Position, blk world.Block) { b.inst.SetBlock(p, blk) }
func (b instanceBridge) GetBlock(p world.Position) world.Block      { return b.inst.World.GetBlock(p) }
func (b instanceBridge) SetBlocks(changes []world.BlockChange)      { b.inst.SetBlocks(changes) }
func (b instanceBridge) BroadcastChat(sender, msg string)           { b.inst.BroadcastChat(sender, msg) }
func (b instanceBridge) PlayerCount() int                           { return b.inst.Players.Count() }
func (b instanceBridge) SetPvP(enabled bool)                        { b.inst.SetPvP(enabled) }
func (b instanceBridge) SetInstantRespawn(enabled bool)             { b.inst.SetInstantRespawn(enabled) }

func (b instanceBridge) DropItem(x, y, z float64, itemName string, count int) bool {
	return b.inst.DropItem(x, y, z, itemName, count)
}

func (b instanceBridge) DroppedItemsNear(x, y, z, radius float64, itemName string) int {
	return b.inst.DroppedItemsNear(x, y, z, radius, itemName)
}

// SpawnHologram returns nil (not a typed-nil handle) when the instance can't
// allocate entities, so games can nil-check the result.
func (b instanceBridge) SpawnHologram(x, y, z float64, text string) game.Hologram {
	h := b.inst.SpawnHologram(x, y, z, text)
	if h == nil {
		return nil
	}
	return h
}

func (b instanceBridge) Players() []game.PlayerHandle {
	conns := b.inst.Players.snapshot()
	out := make([]game.PlayerHandle, 0, len(conns))
	for _, c := range conns {
		out = append(out, handle(c))
	}
	return out
}

func (b instanceBridge) PlayerByName(name string) (game.PlayerHandle, bool) {
	c, ok := b.inst.Players.ByName(name)
	if !ok {
		return nil, false
	}
	return handle(c), true
}

func (b instanceBridge) SetCustomRespawn(enabled bool)    { b.inst.SetCustomRespawn(enabled) }
func (b instanceBridge) SetWeaponDamage(enabled bool)     { b.inst.SetWeaponDamage(enabled) }
func (b instanceBridge) SetTNTAutoPrime(enabled bool)     { b.inst.SetTNTAutoPrime(enabled) }
func (b instanceBridge) SetExplosionDamage(scale float64) { b.inst.SetExplosionDamage(scale) }
func (b instanceBridge) FreezeTime(dayTime int64)         { b.inst.FreezeTime(dayTime) }

func (b instanceBridge) PlaySound(name string, x, y, z float64, volume, pitch float32) {
	b.inst.playSound(name, sound.CategoryPlayer, x, y, z, volume, pitch)
}

// ThrowProjectile launches item from p (a handle of this server).
func (b instanceBridge) ThrowProjectile(p game.PlayerHandle, item string, speed float64, hooks game.ProjectileHooks) {
	gp, ok := p.(gamebridge.Player)
	if !ok {
		return
	}
	c, ok := gp.C.(*ClientConnection)
	if !ok {
		return
	}
	b.inst.launchProjectile(c, item, speed, hooks.OnTick, hooks.OnImpact)
}

// EndGame moves every player in the instance to the hub, then removes the
// instance. Safe to call from any hook; the actual teardown happens
// asynchronously so it doesn't deadlock with a running hook.
func (b instanceBridge) EndGame() {
	// Don't tear ourselves down synchronously inside a hook — the hook
	// holds a goroutine that we'd then need to keep alive long enough to
	// return. Spawn a teardown goroutine instead.
	inst := b.inst
	srv := b.server
	go func() {
		// Move every player to the hub. MovePlayer must run on the
		// player's own readLoop goroutine, but we don't have a clean way
		// to enqueue commands per-connection yet. For now, accept the
		// race — the alternative is letting the instance leak when
		// EndGame is called from a Logic method on a different goroutine
		// than the moving player.
		for _, c := range inst.Players.snapshot() {
			_ = srv.MovePlayer(c, srv.Hub, 0, 80, 0)
		}
		_ = srv.RemoveInstance(inst.ID)
	}()
}

// handle is c as the plugin API's player.
func handle(c *ClientConnection) game.PlayerHandle { return gamebridge.Player{C: c} }

// AttachLogic wires a game's Logic to an Instance's hooks. Call this
// right after creating the instance and before any player joins.
//
// The Ctx passed to every hook is the same instance — Logic
// implementations can stash it (in OnInstanceStart) if they need to call
// back into the server from a goroutine they spawn.
func (s *Server) AttachLogic(inst *Instance, logic game.Logic) *game.Ctx {
	ctx := inst.pluginCtx()

	inst.OnTick(func(tick uint64) {
		logic.OnTick(ctx, tick)
	})
	inst.OnPlayerJoin = func(c *ClientConnection) {
		logic.OnPlayerJoin(ctx, handle(c))
	}
	inst.OnPlayerLeave = func(c *ClientConnection) {
		logic.OnPlayerLeave(ctx, handle(c))
	}
	inst.OnBlockBreak = func(c *ClientConnection, pos world.Position) bool {
		return logic.OnBlockBreak(ctx, handle(c), pos)
	}
	inst.OnBlockPlace = func(c *ClientConnection, pos world.Position, blk world.Block) bool {
		return logic.OnBlockPlace(ctx, handle(c), pos, blk)
	}
	if ev, ok := logic.(game.ExplosionBreakVeto); ok {
		inst.OnExplosionBreak = func(c *ClientConnection, pos world.Position) bool {
			var igniter game.PlayerHandle
			if c != nil {
				igniter = handle(c)
			}
			return ev.OnExplosionBreak(ctx, igniter, pos)
		}
	}
	if bg, ok := logic.(game.BotGuide); ok {
		inst.botGuide = bg
	}
	if fs, ok := logic.(game.ForceStarter); ok {
		inst.OnForceStart = func() error { return fs.ForceStart(ctx) }
	}
	if rw, ok := logic.(game.PlacementRewriter); ok {
		inst.OnRewritePlace = func(c *ClientConnection, pos world.Position, blk world.Block) world.Block {
			return rw.RewritePlacedBlock(ctx, handle(c), pos, blk)
		}
	}
	inst.OnBlockInteract = func(c *ClientConnection, click game.BlockInteraction) bool {
		return logic.OnBlockInteract(ctx, handle(c), click)
	}
	inst.OnEntityInteract = func(c *ClientConnection, ei game.EntityInteraction) bool {
		return logic.OnEntityInteract(ctx, handle(c), ei)
	}
	inst.OnChat = func(c *ClientConnection, msg string) (string, bool) {
		return logic.OnChat(ctx, handle(c), msg)
	}
	inst.OnPlayerAttack = func(attacker, target *ClientConnection) bool {
		return logic.OnPlayerAttack(ctx, handle(attacker), handle(target))
	}
	inst.OnPlayerDeath = func(victim, killer *ClientConnection) {
		// killer is nil for environmental deaths — pass a nil PlayerHandle
		// (not a handle wrapping nil) so the game's nil check works.
		var killerHandle game.PlayerHandle
		if killer != nil {
			killerHandle = handle(killer)
		}
		logic.OnPlayerDeath(ctx, handle(victim), killerHandle)
	}
	inst.OnItemUse = func(c *ClientConnection, use game.ItemUse) bool {
		return logic.OnItemUse(ctx, handle(c), use)
	}
	inst.OnItemConsume = func(c *ClientConnection, st inventory.Stack) bool {
		return logic.OnItemConsume(ctx, handle(c), gamebridge.ToGame(st, -1))
	}
	inst.OnStop = func() {
		logic.OnInstanceEnd(ctx)
	}

	// Fire OnInstanceStart synchronously — by the time AttachLogic
	// returns, the Logic has seen its lifecycle begin.
	safeHook(inst, "OnInstanceStart", func() { logic.OnInstanceStart(ctx) })
	return ctx
}

// StartGame is the high-level entry: look up a registered game by ID,
// instantiate its template into a new Instance, attach the Logic, and
// register the instance under a uniquified ID. Matchmaker calls this; for
// now we expose it directly so /game start can drive it.
func (s *Server) StartGame(defID string) (*Instance, error) {
	def, ok := game.GetDef(defID)
	if !ok {
		return nil, fmt.Errorf("no game registered: %s", defID)
	}
	if def.Template == nil {
		return nil, fmt.Errorf("game %s has no template", defID)
	}
	id := fmt.Sprintf("%s-%d", def.ID, s.nextInstanceSerial())
	inst := NewInstance(id, s, def.Template.Instantiate())
	inst.GameID = def.ID
	// Ephemeral: normally EndGame removes the round, but if every player
	// disconnects instead, the janitor collects the leftover.
	markEphemeral(inst, time.Now())
	s.AddInstance(inst)

	logic := def.New()
	if logic == nil {
		_ = s.RemoveInstance(id)
		return nil, fmt.Errorf("game %s factory returned nil Logic", defID)
	}
	s.AttachLogic(inst, logic)
	return inst, nil
}
