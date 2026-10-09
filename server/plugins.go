package server

import (
	"fmt"
	"log/slog"
	"strings"

	"minecraft-server/game"
	"minecraft-server/world"
)

// plugins.go is where the server-wide plugin API (game/plugin.go) meets the
// per-instance hook fields on Instance.
//
// Every instance carries a game.Ctx and the snapshot of registered
// listeners taken when it was created. The core never calls the hook fields
// (OnBlockBreak, OnChat, …) directly any more — it goes through the fire*/
// allow* methods below, which run every listener first and the instance's
// own hook (the attached game Logic, or the hub/lobby closures) last. Veto
// hooks short-circuit on the first false, so a listener can consume an
// event before the game sees it.

// pluginCtx returns the instance's game.Ctx, creating it lazily for
// instances built without a server (tests).
func (i *Instance) pluginCtx() *game.Ctx {
	if i.ctx == nil {
		i.ctx = &game.Ctx{InstanceID: i.ID, Instance: instanceBridge{server: i.Server, inst: i}}
	}
	return i.ctx
}

// initListeners snapshots the registered listeners and fires their
// OnInstanceStart. Called once from NewInstance; the tick fan-out is
// subscribed there too.
func (i *Instance) initListeners() {
	i.listeners = game.Listeners()
	ctx := i.pluginCtx()
	for _, l := range i.listeners {
		l := l
		safeHook(i, "listener "+l.Name+" OnInstanceStart", func() { l.Logic.OnInstanceStart(ctx) })
	}
	if len(i.listeners) > 0 {
		i.OnTick(func(tick uint64) {
			for _, l := range i.listeners {
				l.Logic.OnTick(ctx, tick)
			}
		})
	}
}

// fireStop runs listeners' OnInstanceEnd, then the instance's own OnStop.
func (i *Instance) fireStop() {
	ctx := i.pluginCtx()
	for _, l := range i.listeners {
		l := l
		safeHook(i, "listener "+l.Name+" OnInstanceEnd", func() { l.Logic.OnInstanceEnd(ctx) })
	}
	if i.OnStop != nil {
		safeHook(i, "OnStop", i.OnStop)
	}
}

func (i *Instance) fireJoin(c *ClientConnection) {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	for _, l := range i.listeners {
		l := l
		safeHook(i, "listener "+l.Name+" OnPlayerJoin", func() { l.Logic.OnPlayerJoin(ctx, p) })
	}
	if hook := i.OnPlayerJoin; hook != nil {
		safeHook(i, "OnPlayerJoin", func() { hook(c) })
	}
}

func (i *Instance) fireLeave(c *ClientConnection) {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	for _, l := range i.listeners {
		l := l
		safeHook(i, "listener "+l.Name+" OnPlayerLeave", func() { l.Logic.OnPlayerLeave(ctx, p) })
	}
	if hook := i.OnPlayerLeave; hook != nil {
		safeHook(i, "OnPlayerLeave", func() { hook(c) })
	}
}

// allowBlockBreak: listeners first, then the instance hook. nil hook = allow.
func (i *Instance) allowBlockBreak(c *ClientConnection, pos world.Position) bool {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	for _, l := range i.listeners {
		if !safeVeto(i, "listener "+l.Name+" OnBlockBreak", func() bool { return l.Logic.OnBlockBreak(ctx, p, pos) }) {
			return false
		}
	}
	if hook := i.OnBlockBreak; hook != nil {
		return hook(c, pos)
	}
	return true
}

// rewritePlacedBlock lets listeners, then the instance's logic, swap the
// block about to be placed (game.PlacementRewriter). Listeners that don't
// implement the interface are skipped.
func (i *Instance) rewritePlacedBlock(c *ClientConnection, pos world.Position, blk world.Block) world.Block {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	for _, l := range i.listeners {
		rw, ok := l.Logic.(game.PlacementRewriter)
		if !ok {
			continue
		}
		safeHook(i, "listener "+l.Name+" RewritePlacedBlock", func() { blk = rw.RewritePlacedBlock(ctx, p, pos, blk) })
	}
	if hook := i.OnRewritePlace; hook != nil {
		blk = hook(c, pos, blk)
	}
	return blk
}

func (i *Instance) allowBlockPlace(c *ClientConnection, pos world.Position, blk world.Block) bool {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	for _, l := range i.listeners {
		if !safeVeto(i, "listener "+l.Name+" OnBlockPlace", func() bool { return l.Logic.OnBlockPlace(ctx, p, pos, blk) }) {
			return false
		}
	}
	if hook := i.OnBlockPlace; hook != nil {
		return hook(c, pos, blk)
	}
	return true
}

// allowBlockInteract fires OnBlockInteract for a click on a block. false =
// consumed: the caller must not dig/place/open and should roll back the
// client's prediction.
func (i *Instance) allowBlockInteract(c *ClientConnection, click game.BlockInteraction) bool {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	for _, l := range i.listeners {
		if !safeVeto(i, "listener "+l.Name+" OnBlockInteract", func() bool { return l.Logic.OnBlockInteract(ctx, p, click) }) {
			return false
		}
	}
	if hook := i.OnBlockInteract; hook != nil {
		return hook(c, click)
	}
	return true
}

// allowEntityInteract fires OnEntityInteract for a right-click on a world
// entity. false = consumed.
func (i *Instance) allowEntityInteract(c *ClientConnection, ei game.EntityInteraction) bool {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	for _, l := range i.listeners {
		if !safeVeto(i, "listener "+l.Name+" OnEntityInteract", func() bool { return l.Logic.OnEntityInteract(ctx, p, ei) }) {
			return false
		}
	}
	if hook := i.OnEntityInteract; hook != nil {
		return hook(c, ei)
	}
	return true
}

// allowItemUse fires OnItemUse for a right-click in the air. false =
// consumed: the caller must not run bucket/throw/eat behaviour.
func (i *Instance) allowItemUse(c *ClientConnection, use game.ItemUse) bool {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	for _, l := range i.listeners {
		if !safeVeto(i, "listener "+l.Name+" OnItemUse", func() bool { return l.Logic.OnItemUse(ctx, p, use) }) {
			return false
		}
	}
	if hook := i.OnItemUse; hook != nil {
		return hook(c, use)
	}
	return true
}

// fireItemConsume fires OnItemConsume when eating/drinking completes. true =
// some hook handled the effect, so the caller skips the vanilla default.
// Listeners run first; the first handler wins.
func (i *Instance) fireItemConsume(c *ClientConnection, st itemStack) bool {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	gs := toGameStack(st, -1)
	for _, l := range i.listeners {
		handled := false
		safeHook(i, "listener "+l.Name+" OnItemConsume", func() { handled = l.Logic.OnItemConsume(ctx, p, gs) })
		if handled {
			return true
		}
	}
	if hook := i.OnItemConsume; hook != nil {
		return hook(c, st)
	}
	return false
}

func (i *Instance) allowAttack(attacker, target *ClientConnection) bool {
	ctx := i.pluginCtx()
	a, t := playerBridge{conn: attacker}, playerBridge{conn: target}
	for _, l := range i.listeners {
		if !safeVeto(i, "listener "+l.Name+" OnPlayerAttack", func() bool { return l.Logic.OnPlayerAttack(ctx, a, t) }) {
			return false
		}
	}
	if hook := i.OnPlayerAttack; hook != nil {
		return hook(attacker, target)
	}
	return true
}

// filterChat threads the message through every listener's OnChat (each
// sees the previous rewrite), then the instance hook. false = drop.
func (i *Instance) filterChat(c *ClientConnection, msg string) (string, bool) {
	ctx, p := i.pluginCtx(), playerBridge{conn: c}
	for _, l := range i.listeners {
		rewrite, allow := msg, true
		safeHook(i, "listener "+l.Name+" OnChat", func() { rewrite, allow = l.Logic.OnChat(ctx, p, msg) })
		if !allow {
			return "", false
		}
		msg = rewrite
	}
	if hook := i.OnChat; hook != nil {
		return hook(c, msg)
	}
	return msg, true
}

// fireDeath runs listeners' OnPlayerDeath then the instance hook. killer may
// be nil (environmental death).
func (i *Instance) fireDeath(victim, killer *ClientConnection) {
	ctx := i.pluginCtx()
	v := playerBridge{conn: victim}
	var k game.PlayerHandle
	if killer != nil {
		k = playerBridge{conn: killer}
	}
	for _, l := range i.listeners {
		l := l
		safeHook(i, "listener "+l.Name+" OnPlayerDeath", func() { l.Logic.OnPlayerDeath(ctx, v, k) })
	}
	if hook := i.OnPlayerDeath; hook != nil {
		safeHook(i, "OnPlayerDeath", func() { hook(victim, killer) })
	}
}

// safeVeto runs a bool hook under panic recovery. A panicking hook counts
// as "allow" so one broken plugin can't lock every block on the server.
func safeVeto(i *Instance, name string, fn func() bool) (allow bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("hook panic", "instance", i.ID, "hook", name, "panic", fmt.Sprint(r))
			allow = true
		}
	}()
	return fn()
}

// --- plugin commands --------------------------------------------------------

// pluginCommand wraps a game.Command as a server Command so dispatch, /help,
// tab-completion and the brigadier tree treat it like a built-in. Built-in
// names always win: a plugin command shadowed by a built-in is unreachable
// (and logged once at startup by warnShadowedPluginCommands).
func pluginCommand(pc *game.Command) *Command {
	return &Command{
		Name:    pc.Name,
		Aliases: pc.Aliases,
		NeedsOp: pc.NeedsOp,
		Help:    pc.Help,
		plugin:  pc,
		Run: func(c *ClientConnection, args []string) {
			if c.instance == nil {
				return
			}
			defer func() {
				if r := recover(); r != nil {
					slog.Error("plugin command panic", "command", pc.Name, "player", c.playerName, "panic", fmt.Sprint(r))
					_ = c.sendSystemMessage("Command failed: internal error")
				}
			}()
			pc.Run(c.instance.pluginCtx(), playerBridge{conn: c}, args)
		},
	}
}

// lookupCommand resolves a typed command name: built-ins first, then plugin
// commands.
func lookupCommand(name string) (*Command, bool) {
	name = strings.ToLower(name)
	if cmd, ok := commandRegistry[name]; ok {
		return cmd, true
	}
	if pc, ok := game.LookupCommand(name); ok {
		return pluginCommand(pc), true
	}
	return nil, false
}

// pluginCommands returns a server Command wrapper for every plugin command
// whose name isn't taken by a built-in.
func pluginCommands() []*Command {
	var out []*Command
	for _, pc := range game.Commands() {
		if _, shadowed := commandRegistry[strings.ToLower(pc.Name)]; shadowed {
			continue
		}
		out = append(out, pluginCommand(pc))
	}
	return out
}

// warnShadowedPluginCommands logs plugin commands that a built-in hides.
// Called from New so the operator sees it at boot.
func warnShadowedPluginCommands() {
	for _, pc := range game.Commands() {
		for _, key := range append([]string{pc.Name}, pc.Aliases...) {
			if _, shadowed := commandRegistry[strings.ToLower(key)]; shadowed {
				slog.Warn("plugin command shadowed by built-in", "command", pc.Name, "name", key)
			}
		}
	}
}

// pluginComplete asks a plugin command for tab-completion candidates.
// args are the typed words after the command name; the last one is the
// word being completed ("" when the cursor follows a space).
func pluginComplete(c *ClientConnection, cmd *Command, args []string) []string {
	if cmd.plugin == nil || cmd.plugin.Complete == nil || c.instance == nil {
		return nil
	}
	var out []string
	safeHook(c.instance, "plugin complete "+cmd.Name, func() {
		out = cmd.plugin.Complete(c.instance.pluginCtx(), playerBridge{conn: c}, args)
	})
	return out
}
