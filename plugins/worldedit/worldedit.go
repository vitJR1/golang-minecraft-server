// Package worldedit is a small WorldEdit-style region editor and the
// reference plugin for the server-wide plugin API (game/plugin.go). It
// shows every extension point in use:
//
//   - game.RegisterListener: a Logic attached to EVERY instance, so the wand
//     works in the hub, lobbies and arenas alike (OnBlockInteract consumes
//     wand clicks before the game logic treats them as digs/placements;
//     OnInstanceEnd drops undo history for torn-down instances).
//   - game.RegisterCommand: "//pos1", "//set stone" and friends, dispatched
//     with the caller's current instance as the Ctx.
//   - Instance.SetBlocks: region fills go out as per-section packets.
//   - schem.FromWorld / SaveFile: "//schem save <name>" writes a .schem.
//
// Everything is op-only. Selections and clipboards live per player name and
// survive instance moves; undo history is per (player, instance).
package worldedit

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"minecraft-server/game"
	"minecraft-server/schem"
	"minecraft-server/templates"
	"minecraft-server/world"
)

const (
	// Wand is the item that selects corners: left click = pos1, right = pos2.
	Wand = "minecraft:wooden_axe"

	// MaxBlocks caps a single operation so a typo'd selection can't stall
	// the instance's tick loop while it fills a million blocks.
	MaxBlocks = 250_000

	// maxUndo is how many operations per player+instance are kept.
	maxUndo = 20
)

// SchemDir is where "//schem save" writes, relative to the working dir.
var SchemDir = filepath.Join(templates.Root, "worldedit")

// session is one player's editor state.
type session struct {
	pos1, pos2 *world.Position
	clipboard  *clipboard
	undo       map[string][][]world.BlockChange // instance ID → stack of "before" snapshots
}

type clipboard struct {
	blocks []world.BlockChange // relative to the copy origin
}

type plugin struct {
	game.NoopLogic

	mu       sync.Mutex
	sessions map[string]*session // player name → state
}

var we = &plugin{sessions: map[string]*session{}}

func init() {
	game.RegisterListener("worldedit", we)
	for _, c := range commands() {
		game.RegisterCommand(c)
	}
}

// --- listener hooks --------------------------------------------------------

// OnBlockInteract turns wand clicks by ops into selection corners and
// consumes them, so the click neither digs nor places.
func (p *plugin) OnBlockInteract(_ *game.Ctx, pl game.PlayerHandle, click game.BlockInteraction) bool {
	if click.Item != Wand || !pl.IsOp() {
		return true
	}
	s := p.session(pl.Name())
	p.mu.Lock()
	pos := click.Pos
	if click.Action == game.LeftClick {
		s.pos1 = &pos
	} else {
		s.pos2 = &pos
	}
	p.mu.Unlock()
	pl.SendMessage(p.describeCorner(click.Action, pos, s))
	return false
}

// OnInstanceEnd forgets every undo stack that pointed at the torn-down
// instance.
func (p *plugin) OnInstanceEnd(ctx *game.Ctx) {
	p.mu.Lock()
	for _, s := range p.sessions {
		delete(s.undo, ctx.InstanceID)
	}
	p.mu.Unlock()
}

// --- commands --------------------------------------------------------------

func commands() []*game.Command {
	return []*game.Command{
		{Name: "/wand", NeedsOp: true, Help: "//wand — get the selection wand (wooden axe: left = pos1, right = pos2)",
			Run: func(_ *game.Ctx, pl game.PlayerHandle, _ []string) {
				pl.GiveItem(Wand, 1)
				pl.SendMessage("Wand: left-click = pos1, right-click = pos2.")
			}},
		{Name: "/pos1", NeedsOp: true, Help: "//pos1 — set selection corner 1 at your feet",
			Run: func(_ *game.Ctx, pl game.PlayerHandle, _ []string) { we.setCornerAtFeet(pl, game.LeftClick) }},
		{Name: "/pos2", NeedsOp: true, Help: "//pos2 — set selection corner 2 at your feet",
			Run: func(_ *game.Ctx, pl game.PlayerHandle, _ []string) { we.setCornerAtFeet(pl, game.RightClick) }},
		{Name: "/size", NeedsOp: true, Help: "//size — show the current selection",
			Run: func(_ *game.Ctx, pl game.PlayerHandle, _ []string) {
				lo, hi, ok := we.selection(pl.Name())
				if !ok {
					pl.SendMessage("No selection — use the wand or //pos1 //pos2.")
					return
				}
				pl.SendMessage(fmt.Sprintf("Selection %v → %v, %d blocks.", lo, hi, volume(lo, hi)))
			}},
		{Name: "/set", NeedsOp: true, Help: "//set <block> — fill the selection",
			Run:      func(ctx *game.Ctx, pl game.PlayerHandle, args []string) { we.cmdSet(ctx, pl, args) },
			Complete: completeBlock},
		{Name: "/replace", NeedsOp: true, Help: "//replace <from> <to> — replace one block with another in the selection",
			Run:      func(ctx *game.Ctx, pl game.PlayerHandle, args []string) { we.cmdReplace(ctx, pl, args) },
			Complete: completeBlock},
		{Name: "/undo", NeedsOp: true, Help: "//undo — revert your last edit in this instance",
			Run: func(ctx *game.Ctx, pl game.PlayerHandle, _ []string) { we.cmdUndo(ctx, pl) }},
		{Name: "/copy", NeedsOp: true, Help: "//copy — copy the selection to your clipboard (relative to where you stand)",
			Run: func(ctx *game.Ctx, pl game.PlayerHandle, _ []string) { we.cmdCopy(ctx, pl) }},
		{Name: "/paste", NeedsOp: true, Help: "//paste — paste the clipboard at your position",
			Run: func(ctx *game.Ctx, pl game.PlayerHandle, _ []string) { we.cmdPaste(ctx, pl) }},
		{Name: "/schem", NeedsOp: true, Help: "//schem save <name> — write the selection to schem/templates/worldedit/<name>.schem",
			Run: func(ctx *game.Ctx, pl game.PlayerHandle, args []string) { we.cmdSchem(ctx, pl, args) },
			Complete: func(_ *game.Ctx, _ game.PlayerHandle, args []string) []string {
				if len(args) == 1 {
					return []string{"save"}
				}
				return nil
			}},
	}
}

func (p *plugin) cmdSet(ctx *game.Ctx, pl game.PlayerHandle, args []string) {
	if len(args) != 1 {
		pl.SendMessage("Usage: //set <block>")
		return
	}
	blk, err := ParseBlock(args[0])
	if err != nil {
		pl.SendMessage(err.Error())
		return
	}
	lo, hi, ok := p.selection(pl.Name())
	if !ok {
		pl.SendMessage("No selection — use the wand or //pos1 //pos2.")
		return
	}
	if n := volume(lo, hi); n > MaxBlocks {
		pl.SendMessage(fmt.Sprintf("Selection too big: %d blocks (max %d).", n, MaxBlocks))
		return
	}
	var changes []world.BlockChange
	forEachIn(lo, hi, func(pos world.Position) {
		changes = append(changes, world.BlockChange{Pos: pos, Block: blk})
	})
	n := p.apply(ctx, pl.Name(), changes)
	pl.SendMessage(fmt.Sprintf("Set %d blocks to %s.", n, blk.Name))
}

func (p *plugin) cmdReplace(ctx *game.Ctx, pl game.PlayerHandle, args []string) {
	if len(args) != 2 {
		pl.SendMessage("Usage: //replace <from> <to>")
		return
	}
	from, err := ParseBlock(args[0])
	if err != nil {
		pl.SendMessage(err.Error())
		return
	}
	to, err := ParseBlock(args[1])
	if err != nil {
		pl.SendMessage(err.Error())
		return
	}
	lo, hi, ok := p.selection(pl.Name())
	if !ok {
		pl.SendMessage("No selection — use the wand or //pos1 //pos2.")
		return
	}
	if n := volume(lo, hi); n > MaxBlocks {
		pl.SendMessage(fmt.Sprintf("Selection too big: %d blocks (max %d).", n, MaxBlocks))
		return
	}
	var changes []world.BlockChange
	forEachIn(lo, hi, func(pos world.Position) {
		// Match on the block kind, ignoring state properties, so
		// "//replace oak_stairs stone" catches every facing.
		if ctx.Instance.GetBlock(pos).Name == from.Name {
			changes = append(changes, world.BlockChange{Pos: pos, Block: to})
		}
	})
	n := p.apply(ctx, pl.Name(), changes)
	pl.SendMessage(fmt.Sprintf("Replaced %d blocks.", n))
}

func (p *plugin) cmdUndo(ctx *game.Ctx, pl game.PlayerHandle) {
	s := p.session(pl.Name())
	p.mu.Lock()
	stack := s.undo[ctx.InstanceID]
	if len(stack) == 0 {
		p.mu.Unlock()
		pl.SendMessage("Nothing to undo here.")
		return
	}
	before := stack[len(stack)-1]
	s.undo[ctx.InstanceID] = stack[:len(stack)-1]
	p.mu.Unlock()

	ctx.Instance.SetBlocks(before)
	pl.SendMessage(fmt.Sprintf("Undid %d blocks.", len(before)))
}

func (p *plugin) cmdCopy(ctx *game.Ctx, pl game.PlayerHandle) {
	lo, hi, ok := p.selection(pl.Name())
	if !ok {
		pl.SendMessage("No selection — use the wand or //pos1 //pos2.")
		return
	}
	if n := volume(lo, hi); n > MaxBlocks {
		pl.SendMessage(fmt.Sprintf("Selection too big: %d blocks (max %d).", n, MaxBlocks))
		return
	}
	origin := feet(pl)
	cb := &clipboard{}
	forEachIn(lo, hi, func(pos world.Position) {
		cb.blocks = append(cb.blocks, world.BlockChange{
			Pos:   world.Position{X: pos.X - origin.X, Y: pos.Y - origin.Y, Z: pos.Z - origin.Z},
			Block: ctx.Instance.GetBlock(pos),
		})
	})
	s := p.session(pl.Name())
	p.mu.Lock()
	s.clipboard = cb
	p.mu.Unlock()
	pl.SendMessage(fmt.Sprintf("Copied %d blocks.", len(cb.blocks)))
}

func (p *plugin) cmdPaste(ctx *game.Ctx, pl game.PlayerHandle) {
	s := p.session(pl.Name())
	p.mu.Lock()
	cb := s.clipboard
	p.mu.Unlock()
	if cb == nil {
		pl.SendMessage("Clipboard is empty — //copy first.")
		return
	}
	origin := feet(pl)
	changes := make([]world.BlockChange, len(cb.blocks))
	for i, b := range cb.blocks {
		changes[i] = world.BlockChange{
			Pos:   world.Position{X: origin.X + b.Pos.X, Y: origin.Y + b.Pos.Y, Z: origin.Z + b.Pos.Z},
			Block: b.Block,
		}
	}
	n := p.apply(ctx, pl.Name(), changes)
	pl.SendMessage(fmt.Sprintf("Pasted %d blocks.", n))
}

func (p *plugin) cmdSchem(ctx *game.Ctx, pl game.PlayerHandle, args []string) {
	if len(args) != 2 || args[0] != "save" || strings.ContainsAny(args[1], `/\`) {
		pl.SendMessage("Usage: //schem save <name>")
		return
	}
	lo, hi, ok := p.selection(pl.Name())
	if !ok {
		pl.SendMessage("No selection — use the wand or //pos1 //pos2.")
		return
	}
	if n := volume(lo, hi); n > MaxBlocks {
		pl.SendMessage(fmt.Sprintf("Selection too big: %d blocks (max %d).", n, MaxBlocks))
		return
	}
	s := schem.FromWorld(instanceWorld{ctx.Instance}, lo, hi)
	path := filepath.Join(SchemDir, args[1]+".schem")
	if err := schem.SaveFile(path, s); err != nil {
		pl.SendMessage("Save failed: " + err.Error())
		return
	}
	pl.SendMessage("Saved " + path + " (" + fmt.Sprint(volume(lo, hi)) + " blocks).")
}

// --- helpers ---------------------------------------------------------------

// apply records the "before" state for undo, applies the changes through the
// bulk API, and returns how many blocks were written.
func (p *plugin) apply(ctx *game.Ctx, player string, changes []world.BlockChange) int {
	if len(changes) == 0 {
		return 0
	}
	before := make([]world.BlockChange, len(changes))
	for i, ch := range changes {
		before[i] = world.BlockChange{Pos: ch.Pos, Block: ctx.Instance.GetBlock(ch.Pos)}
	}
	s := p.session(player)
	p.mu.Lock()
	stack := append(s.undo[ctx.InstanceID], before)
	if len(stack) > maxUndo {
		stack = stack[len(stack)-maxUndo:]
	}
	s.undo[ctx.InstanceID] = stack
	p.mu.Unlock()

	ctx.Instance.SetBlocks(changes)
	return len(changes)
}

func (p *plugin) session(name string) *session {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[name]
	if !ok {
		s = &session{undo: map[string][][]world.BlockChange{}}
		p.sessions[name] = s
	}
	return s
}

// selection returns the player's box as (min, max) corners.
func (p *plugin) selection(name string) (lo, hi world.Position, ok bool) {
	s := p.session(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.pos1 == nil || s.pos2 == nil {
		return lo, hi, false
	}
	a, b := *s.pos1, *s.pos2
	lo = world.Position{X: min(a.X, b.X), Y: min(a.Y, b.Y), Z: min(a.Z, b.Z)}
	hi = world.Position{X: max(a.X, b.X), Y: max(a.Y, b.Y), Z: max(a.Z, b.Z)}
	return lo, hi, true
}

func (p *plugin) setCornerAtFeet(pl game.PlayerHandle, which game.ClickAction) {
	pos := feet(pl)
	s := p.session(pl.Name())
	p.mu.Lock()
	if which == game.LeftClick {
		s.pos1 = &pos
	} else {
		s.pos2 = &pos
	}
	p.mu.Unlock()
	pl.SendMessage(p.describeCorner(which, pos, s))
}

func (p *plugin) describeCorner(which game.ClickAction, pos world.Position, s *session) string {
	n := 1
	if which == game.RightClick {
		n = 2
	}
	msg := fmt.Sprintf("Position %d set to (%d, %d, %d).", n, pos.X, pos.Y, pos.Z)
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.pos1 != nil && s.pos2 != nil {
		lo := world.Position{X: min(s.pos1.X, s.pos2.X), Y: min(s.pos1.Y, s.pos2.Y), Z: min(s.pos1.Z, s.pos2.Z)}
		hi := world.Position{X: max(s.pos1.X, s.pos2.X), Y: max(s.pos1.Y, s.pos2.Y), Z: max(s.pos1.Z, s.pos2.Z)}
		msg += fmt.Sprintf(" Selection: %d blocks.", volume(lo, hi))
	}
	return msg
}

// feet is the block the player is standing in.
func feet(pl game.PlayerHandle) world.Position {
	s := pl.Pose()
	return world.Position{X: floor(s.X), Y: floor(s.Y), Z: floor(s.Z)}
}

func floor(v float64) int {
	i := int(v)
	if float64(i) > v {
		i--
	}
	return i
}

func volume(lo, hi world.Position) int {
	return (hi.X - lo.X + 1) * (hi.Y - lo.Y + 1) * (hi.Z - lo.Z + 1)
}

func forEachIn(lo, hi world.Position, fn func(world.Position)) {
	for y := lo.Y; y <= hi.Y; y++ {
		for z := lo.Z; z <= hi.Z; z++ {
			for x := lo.X; x <= hi.X; x++ {
				fn(world.Position{X: x, Y: y, Z: z})
			}
		}
	}
}

// ParseBlock resolves "stone", "minecraft:stone" or
// "oak_stairs[facing=north,half=top]" into a Block, property-aware.
func ParseBlock(spec string) (world.Block, error) {
	name, propStr := spec, ""
	if i := strings.IndexByte(spec, '['); i >= 0 && strings.HasSuffix(spec, "]") {
		name, propStr = spec[:i], spec[i+1:len(spec)-1]
	}
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	blk, ok := world.BlockByName(name)
	if !ok {
		return world.Block{}, fmt.Errorf("unknown block %q", spec)
	}
	if propStr == "" {
		return blk, nil
	}
	props := map[string]string{}
	for _, kv := range strings.Split(propStr, ",") {
		k, v, found := strings.Cut(strings.TrimSpace(kv), "=")
		if !found {
			return world.Block{}, fmt.Errorf("bad property %q in %q", kv, spec)
		}
		props[k] = v
	}
	blk.StateID = world.ResolveStateID(name, props)
	return blk, nil
}

// completeBlock offers block names matching the typed prefix (with or
// without the "minecraft:" namespace).
func completeBlock(_ *game.Ctx, _ game.PlayerHandle, args []string) []string {
	if len(args) == 0 {
		return nil
	}
	prefix := strings.ToLower(args[len(args)-1])
	var out []string
	for _, name := range world.KnownBlockNames() {
		short := strings.TrimPrefix(name, "minecraft:")
		if strings.HasPrefix(short, prefix) || strings.HasPrefix(name, prefix) {
			out = append(out, short)
		}
	}
	sort.Strings(out)
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

// instanceWorld adapts a game.Instance to world.World for schem.FromWorld.
// Range isn't part of the plugin surface; FromWorld only needs GetBlock.
type instanceWorld struct{ game.Instance }

func (w instanceWorld) Range(func(world.Position, world.Block)) {}
