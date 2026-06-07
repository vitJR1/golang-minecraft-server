package game

import (
	"fmt"
	"sort"
	"sync"

	"minecraft-server/world"
)

// ArenaBuilder turns a world template plus a JSON layout config into a ready
// game Definition for one named arena. A game kind (e.g. "bedwars") registers
// one builder so the server's /arena create command can spin up new playable
// arenas at runtime without the server knowing the game's internals.
//
//   - arenaID is the unique id the matchmaker will queue under (also the
//     Definition.ID); displayName is the human label.
//   - tmpl is the (already loaded, centered) map blocks; the builder should
//     clone it before mutating.
//   - config is the raw bytes of the arena's JSON layout file (spawns, beds,
//     generators, villagers). The format is the game's own; the server just
//     hands it over.
type ArenaBuilder func(arenaID, displayName string, tmpl *world.Template, config []byte) (*Definition, error)

var (
	arenaMu       sync.RWMutex
	arenaBuilders = map[string]ArenaBuilder{}
)

// RegisterArenaBuilder registers the arena builder for a game kind. Called
// from a game package's init(). Panics on duplicate/empty — a programmer
// error like Register.
func RegisterArenaBuilder(kind string, b ArenaBuilder) {
	if kind == "" || b == nil {
		panic("game.RegisterArenaBuilder: empty kind or nil builder")
	}
	arenaMu.Lock()
	defer arenaMu.Unlock()
	if _, exists := arenaBuilders[kind]; exists {
		panic(fmt.Sprintf("game.RegisterArenaBuilder %q: duplicate", kind))
	}
	arenaBuilders[kind] = b
}

// GetArenaBuilder returns the builder for a game kind, if one is registered.
func GetArenaBuilder(kind string) (ArenaBuilder, bool) {
	arenaMu.RLock()
	defer arenaMu.RUnlock()
	b, ok := arenaBuilders[kind]
	return b, ok
}

// ArenaKinds returns every game kind that supports /arena create, sorted.
func ArenaKinds() []string {
	arenaMu.RLock()
	defer arenaMu.RUnlock()
	out := make([]string, 0, len(arenaBuilders))
	for k := range arenaBuilders {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
