package server

import (
	"fmt"
	"log/slog"
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
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
func (b instanceBridge) BroadcastChat(sender, msg string)           { b.inst.BroadcastChat(sender, msg) }
func (b instanceBridge) PlayerCount() int                           { return b.inst.Players.Count() }

func (b instanceBridge) Players() []game.PlayerHandle {
	conns := b.inst.Players.snapshot()
	out := make([]game.PlayerHandle, 0, len(conns))
	for _, c := range conns {
		out = append(out, playerBridge{conn: c})
	}
	return out
}

func (b instanceBridge) PlayerByName(name string) (game.PlayerHandle, bool) {
	c, ok := b.inst.Players.ByName(name)
	if !ok {
		return nil, false
	}
	return playerBridge{conn: c}, true
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

// playerBridge implements game.PlayerHandle over *ClientConnection.
type playerBridge struct {
	conn *ClientConnection
}

func (b playerBridge) Name() string          { return b.conn.player.Name }
func (b playerBridge) EntityID() int32       { return b.conn.player.EntityID }
func (b playerBridge) Pose() player.Snapshot { return b.conn.player.Snapshot() }
func (b playerBridge) SendMessage(text string) {
	_ = b.conn.sendSystemMessage(text)
}

func (b playerBridge) Teleport(x, y, z float64) {
	b.conn.player.MoveTo(x, y, z, false)
	_ = b.conn.sendSyncPlayerPosition(x, y, z, 1)
	b.conn.broadcastEntityTeleport()
}

func (b playerBridge) SetGamemode(g player.Gamemode) {
	b.conn.player.SetGamemode(g)
	_ = b.conn.sendGameModeChange(g)
}

// Kick closes the player's connection. The reason is logged but not (yet)
// sent on the wire as a Play Disconnect — that needs CbPlayDisconnect
// which we haven't wired up. Client will see a generic "Connection lost".
func (b playerBridge) Kick(reason string) {
	if reason != "" {
		slog.Info("kicking player", "player", b.conn.playerName, "reason", reason)
	}
	go b.conn.cleanup()
}

// AttachLogic wires a game's Logic to an Instance's hooks. Call this
// right after creating the instance and before any player joins.
//
// The Ctx passed to every hook is the same instance — Logic
// implementations can stash it (in OnInstanceStart) if they need to call
// back into the server from a goroutine they spawn.
func (s *Server) AttachLogic(inst *Instance, logic game.Logic) *game.Ctx {
	ctx := &game.Ctx{
		InstanceID: inst.ID,
		Instance:   instanceBridge{server: s, inst: inst},
	}

	inst.OnTick(func(tick uint64) {
		logic.OnTick(ctx, tick)
	})
	inst.OnPlayerJoin = func(c *ClientConnection) {
		logic.OnPlayerJoin(ctx, playerBridge{conn: c})
	}
	inst.OnPlayerLeave = func(c *ClientConnection) {
		logic.OnPlayerLeave(ctx, playerBridge{conn: c})
	}
	inst.OnBlockBreak = func(c *ClientConnection, pos world.Position) bool {
		return logic.OnBlockBreak(ctx, playerBridge{conn: c}, pos)
	}
	inst.OnBlockPlace = func(c *ClientConnection, pos world.Position, blk world.Block) bool {
		return logic.OnBlockPlace(ctx, playerBridge{conn: c}, pos, blk)
	}
	inst.OnChat = func(c *ClientConnection, msg string) (string, bool) {
		return logic.OnChat(ctx, playerBridge{conn: c}, msg)
	}
	inst.OnStop = func() {
		logic.OnInstanceEnd(ctx)
	}

	// Fire OnInstanceStart synchronously — by the time AttachLogic
	// returns, the Logic has seen its lifecycle begin.
	logic.OnInstanceStart(ctx)
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
	s.AddInstance(inst)

	logic := def.New()
	if logic == nil {
		_ = s.RemoveInstance(id)
		return nil, fmt.Errorf("game %s factory returned nil Logic", defID)
	}
	s.AttachLogic(inst, logic)
	return inst, nil
}
