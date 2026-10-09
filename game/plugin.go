package game

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"minecraft-server/world"
)

// plugin.go is the server-wide half of the plugin API. Where Definition +
// Logic describe ONE mini-game running in ONE instance, the pieces here let
// a plugin act across every instance (hub, lobbies, arenas) at once:
//
//   - RegisterListener attaches a Logic to every instance the server creates
//     (a WorldEdit-style tool, a region protector, a stats collector, …).
//   - RegisterCommand adds a slash command that runs with the caller's
//     current instance as its Ctx.
//   - BlockInteraction is the payload of the OnBlockInteract hook: a click
//     on a block with (or without) an item in hand, before the core decides
//     whether it's a dig, a placement, or a container open.
//
// Everything is registered from init() and read by the server afterwards;
// like Register, duplicates and empty names panic at startup.

// Listener is a Logic that the server attaches to EVERY instance alongside
// the instance's own game Logic. OnInstanceStart/End fire per instance as
// they are created/torn down; every other hook fires for the instance the
// event happened in, with that instance's Ctx.
//
// Ordering: listeners run before the instance's own Logic, in registration
// order. For veto hooks (OnBlockBreak/Place/Interact, OnPlayerAttack) the
// first false wins and later hooks — including the game's — don't run, so a
// tool plugin can consume a click before a game treats it as a dig. OnChat
// composes: each listener sees the previous one's rewrite.
//
// A listener must be safe for concurrent use — different instances call it
// from different goroutines.
type Listener struct {
	Name  string
	Logic Logic
}

var (
	listenerMu sync.RWMutex
	listeners  []Listener
)

// RegisterListener registers l under name. Call from init(): instances
// created before registration never see the listener.
func RegisterListener(name string, l Logic) {
	if name == "" || l == nil {
		panic("game.RegisterListener: empty name or nil logic")
	}
	listenerMu.Lock()
	defer listenerMu.Unlock()
	for _, existing := range listeners {
		if existing.Name == name {
			panic(fmt.Sprintf("game.RegisterListener %q: duplicate", name))
		}
	}
	listeners = append(listeners, Listener{Name: name, Logic: l})
}

// Listeners returns a snapshot of every registered listener, in registration
// order.
func Listeners() []Listener {
	listenerMu.RLock()
	defer listenerMu.RUnlock()
	out := make([]Listener, len(listeners))
	copy(out, listeners)
	return out
}

// Command is a slash command contributed by a plugin. It is dispatched with
// the caller's current instance (so Run can read/write that world through
// ctx.Instance) and the caller as a PlayerHandle.
//
// Names are matched case-insensitively. A name may start with "/" to get a
// WorldEdit-style double-slash command: Name "/set" is typed as "//set".
// Built-in server commands take precedence over plugin commands with the
// same name or alias.
type Command struct {
	// Name is the primary word after the slash; Aliases are alternatives.
	Name    string
	Aliases []string

	// NeedsOp restricts the command to operators (the server's only
	// permission level today).
	NeedsOp bool

	// Help is the one-line usage shown by /help.
	Help string

	// Run executes the command. args are the whitespace-split words after
	// the command name. Panics are recovered and logged by the server.
	Run func(ctx *Ctx, p PlayerHandle, args []string)

	// Complete, if set, supplies tab-completion candidates for the argument
	// at index len(args)-1 (the word being typed, possibly ""). The server
	// keeps only candidates that start with that word. Optional.
	Complete func(ctx *Ctx, p PlayerHandle, args []string) []string
}

var (
	commandMu sync.RWMutex
	commands  = map[string]*Command{} // lowercase name/alias → command
)

// RegisterCommand registers cmd under its name and aliases. Panics on an
// empty name, nil Run, or a name/alias already registered by another
// plugin command.
func RegisterCommand(cmd *Command) {
	if cmd == nil || cmd.Name == "" {
		panic("game.RegisterCommand: nil command or empty name")
	}
	if cmd.Run == nil {
		panic(fmt.Sprintf("game.RegisterCommand %q: nil Run", cmd.Name))
	}
	commandMu.Lock()
	defer commandMu.Unlock()
	for _, key := range append([]string{cmd.Name}, cmd.Aliases...) {
		key = strings.ToLower(key)
		if existing, dup := commands[key]; dup && existing != cmd {
			panic(fmt.Sprintf("game.RegisterCommand %q: %q already registered", cmd.Name, key))
		}
		commands[key] = cmd
	}
}

// LookupCommand finds a plugin command by name or alias (case-insensitive).
func LookupCommand(name string) (*Command, bool) {
	commandMu.RLock()
	defer commandMu.RUnlock()
	c, ok := commands[strings.ToLower(name)]
	return c, ok
}

// Commands returns every plugin command once (aliases collapsed), sorted by
// name.
func Commands() []*Command {
	commandMu.RLock()
	defer commandMu.RUnlock()
	seen := make(map[*Command]bool, len(commands))
	out := make([]*Command, 0, len(commands))
	for _, c := range commands {
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ClickAction distinguishes the two ways a player can click a block.
type ClickAction int

const (
	// LeftClick is the start of a dig (attack button on a block).
	LeftClick ClickAction = iota
	// RightClick is a use on a block (place / open / interact).
	RightClick
)

func (a ClickAction) String() string {
	if a == LeftClick {
		return "left"
	}
	return "right"
}

// BlockInteraction is the payload of Logic.OnBlockInteract: a click on a
// block, delivered BEFORE the core acts on it. Returning false from the
// hook consumes the click — no dig, no placement, no container open — and
// the client's prediction is rolled back.
type BlockInteraction struct {
	Pos    world.Position // the block that was clicked
	Face   int            // clicked face: 0=bottom 1=top 2=north 3=south 4=west 5=east
	Action ClickAction
	Item   string // namespaced id of the held item ("" = empty hand)
}

// Target returns the position a placement would land on: the clicked block
// offset by the clicked face.
func (b BlockInteraction) Target() world.Position {
	p := b.Pos
	switch b.Face {
	case 0:
		p.Y--
	case 1:
		p.Y++
	case 2:
		p.Z--
	case 3:
		p.Z++
	case 4:
		p.X--
	case 5:
		p.X++
	}
	return p
}

// EntityInteraction is the payload of Logic.OnEntityInteract: a right-click
// on a world entity (the instance's baked/added entities, not players).
type EntityInteraction struct {
	EntityID int32
	Type     string  // namespaced entity type, e.g. "minecraft:villager"
	X, Y, Z  float64 // entity position
	Item     string  // namespaced id of the held item ("" = empty hand)
}

// MenuItem is one slot of a PlayerHandle.OpenMenu GUI.
type MenuItem struct {
	Slot  int    // 0 .. rows*9-1
	Item  string // namespaced item id shown in the slot
	Count int    // stack size shown (0 → 1)
	Name  string // hover label
}
