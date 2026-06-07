package game

import (
	"fmt"
	"sync"
)

// registry holds every registered game definition keyed by Definition.ID.
// Populated at process init() time by game packages. Concurrent reads
// from the server (matchmaker, /game list command) go through GetDef and
// All.
var (
	registryMu sync.RWMutex
	registry   = map[string]*Definition{}
)

// Register adds a game definition to the global registry. Typically
// called from an init() function in the game's own package:
//
//	func init() {
//	    game.Register(&game.Definition{
//	        ID:         "ffa",
//	        Name:       "Free-for-all",
//	        MinPlayers: 2,
//	        MaxPlayers: 8,
//	        Template:   buildArena(),
//	        New:        func() game.Logic { return &ffaLogic{} },
//	    })
//	}
//
// Panics on duplicate ID, missing fields, or nil New — these are
// programmer errors that should fail loudly at startup.
func Register(d *Definition) {
	if d == nil {
		panic("game.Register: nil definition")
	}
	if d.ID == "" {
		panic("game.Register: empty ID")
	}
	if d.New == nil {
		panic(fmt.Sprintf("game.Register %q: nil New factory", d.ID))
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[d.ID]; exists {
		panic(fmt.Sprintf("game.Register %q: duplicate ID", d.ID))
	}
	registry[d.ID] = d
}

// TryRegister adds a definition at runtime (e.g. a dynamically-created
// arena), returning an error instead of panicking on a duplicate ID or
// missing fields. Use this for user-driven registration; Register stays the
// fail-loud path for static init().
func TryRegister(d *Definition) error {
	if d == nil || d.ID == "" {
		return fmt.Errorf("game: invalid definition")
	}
	if d.New == nil {
		return fmt.Errorf("game: %q has nil New factory", d.ID)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[d.ID]; exists {
		return fmt.Errorf("game: %q already registered", d.ID)
	}
	registry[d.ID] = d
	return nil
}

// Unregister removes a definition by ID (e.g. tearing down an arena). No-op
// if absent.
func Unregister(id string) {
	registryMu.Lock()
	delete(registry, id)
	registryMu.Unlock()
}

// GetDef returns the definition with this ID, if registered.
func GetDef(id string) (*Definition, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	d, ok := registry[id]
	return d, ok
}

// All returns every registered definition. Order is unspecified.
func All() []*Definition {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*Definition, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	return out
}

// reset wipes the registry. Test-only — production code never touches
// this. Exposed via game_test.go in this package.
func reset() {
	registryMu.Lock()
	registry = map[string]*Definition{}
	registryMu.Unlock()
}
