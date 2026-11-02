// Package lobby is the catalogue of the per-mode lobbies: their instance
// ids, the map template each loads, which games return to which lobby,
// the hub menu's icons and the placeholder arena names a lobby's selector
// lists, and the stone pad used when a lobby's map is missing.
package lobby

import (
	"strings"

	"minecraft-server/templates"
	"minecraft-server/world"
)

// Lobby IDs. Used both as Instance.ID and as the keys the hub menu
// teleports into.
const (
	FFA     = "ffa"
	BedWars = "bedwars"
	SkyWars = "skywars"
)

// ids is the iteration order at startup. Keep stable — the order
// determines log output and tests can rely on it.
var ids = []string{FFA, BedWars, SkyWars}

// IDs returns every lobby id in startup order.
func IDs() []string { return append([]string(nil), ids...) }

// templateNames maps each lobby ID to the world.Template name the
// loadTemplates walk produces for its .schem file under
// schem/templates/<game>/<file>.schem. Keep in sync with the on-disk
// layout — renaming a file means renaming this map.
var templateNames = map[string]string{
	FFA:     templates.FFALobby,
	BedWars: templates.BedwarsLobby,
	SkyWars: templates.SkywarsLobby,
}

// TemplateName is the map template lobby id loads ("" for an unknown id).
func TemplateName(id string) string { return templateNames[id] }

// ForGame maps a game ID (a Definition ID or arena kind) to its mode's
// lobby: every "bedwars…" mode (bedwars, bedwars-1x1, …) to the BedWars
// lobby, and so on. ok is false for a game with no lobby.
func ForGame(gameID string) (string, bool) {
	for _, id := range ids {
		if gameID == id || strings.HasPrefix(gameID, id+"-") {
			return id, true
		}
	}
	return "", false
}

// menuTargets maps a hub menu icon's key to the lobby its click
// teleports the player into.
var menuTargets = map[string]string{
	"ffa": FFA,
	"bw":  BedWars,
	"sw":  SkyWars,
}

// MenuTarget is the lobby behind hub menu icon key.
func MenuTarget(key string) (string, bool) {
	id, ok := menuTargets[key]
	return id, ok
}

// arenaNames maps each lobby ID to the arena names shown in that lobby's
// ender-pearl menu. Membership doubles as "is this instance a game lobby"
// — see Arenas.
//
// Names are placeholders; future matchmaker dispatch will look them up
// in the game's Definition.
var arenaNames = map[string][]string{
	FFA:     {"The Pit", "Coliseum", "Sandbox", "Backalley", "Rooftop", "Tomb"},
	BedWars: {"Garden", "Aquarium", "Volcano", "Junkyard", "Stronghold", "Lighthouse"},
	SkyWars: {"Cumulus", "Stratos", "Nebula", "Vesper", "Aurora", "Eclipse"},
}

// Arenas returns (arenas, true) if instanceID is a game lobby, else
// (nil, false). The boolean is also the predicate "should this instance
// hand out an ender-pearl arena selector".
func Arenas(instanceID string) ([]string, bool) {
	arenas, ok := arenaNames[instanceID]
	return arenas, ok
}

// Is reports whether instanceID is a game lobby.
func Is(instanceID string) bool {
	_, ok := arenaNames[instanceID]
	return ok
}

// DefaultWorld returns a fresh MemoryWorld with an 11×11 stone platform
// one block below the default spawn (y=66). Centred on origin so players
// spawning at (0.5, 67, 0.5) land on a stone block.
func DefaultWorld() world.World {
	w := world.NewMemoryWorld()
	for x := -5; x <= 5; x++ {
		for z := -5; z <= 5; z++ {
			w.SetBlock(world.Position{X: x, Y: 66, Z: z}, world.Stone)
		}
	}
	return w
}
