package server

import (
	"log/slog"
	"minecraft-server/world"
)

// Lobby IDs. Used both as Instance.ID and as the keys the hub menu
// teleports into. Exported so tests / future game packages can move
// players in/out without hard-coding magic strings.
const (
	LobbyFFA     = "ffa"
	LobbyBedWars = "bedwars"
	LobbySkyWars = "skywars"
)

// lobbyTemplateNames maps each lobby ID to the world.Template name the
// loadTemplates walk produces for its .schem file under
// schem/templates/<game>/<file>.schem. Keep in sync with the on-disk
// layout — renaming a file means renaming this map.
var lobbyTemplateNames = map[string]string{
	LobbyFFA:     "ffa/ffa_lobby",
	LobbyBedWars: "bedwars/bedwars_lobby",
	LobbySkyWars: "skywars/skywars_lobby",
}

// lobbyIDs is the iteration order at startup. Keep stable — the order
// determines log output and tests can rely on it.
var lobbyIDs = []string{LobbyFFA, LobbyBedWars, LobbySkyWars}

// SetupLobbies creates the FFA / BedWars / SkyWars holding-pen
// instances and registers them on s. Each is a read-only lobby
// (build/break/PvP vetoed). World comes from the registered .schem
// template if present (loaded earlier by loadTemplates in main),
// otherwise falls back to a bare 11×11 stone pad at y=66.
//
// Calls into SetupLobbies are idempotent: an instance with the same ID
// already registered is left alone (won't overwrite an existing one
// that a test may have prepared).
func SetupLobbies(s *Server) {
	for _, id := range lobbyIDs {
		if s.GetInstance(id) != nil {
			continue
		}
		w := buildLobbyWorld()
		templateName := lobbyTemplateNames[id]
		if tmpl := s.GetTemplate(templateName); tmpl != nil {
			w = tmpl.Instantiate()
			slog.Info("lobby ready", "id", id, "template", templateName)
		} else {
			slog.Info("lobby ready", "id", id, "template", "(default platform)")
		}
		inst := NewInstance(id, s, w)
		installLobbyProtection(inst)
		s.AddInstance(inst)
	}
}

// buildLobbyWorld returns a fresh MemoryWorld with an 11×11 stone
// platform one block below the default spawn (y=66). Centred on origin
// so players spawning at (0.5, 67, 0.5) land on a stone block.
func buildLobbyWorld() world.World {
	w := world.NewMemoryWorld()
	for x := -5; x <= 5; x++ {
		for z := -5; z <= 5; z++ {
			w.SetBlock(world.Position{X: x, Y: 66, Z: z}, world.Stone)
		}
	}
	return w
}

// installLobbyProtection wires the standard hooks: no breaking, no
// placing, no PvP. Same shape as SetupHubMenu's hub-protection block.
// Also installs OnPlayerJoin so every entry to the lobby restocks the
// hotbar with the navigator (slot 0) + the arena selector (slot 1).
// MovePlayer doesn't send Set Container Slot itself, so without this
// the player's hotbar would be empty after the Respawn.
func installLobbyProtection(inst *Instance) {
	inst.OnBlockBreak = func(c *ClientConnection, _ world.Position) bool {
		_ = c.sendSystemMessage("Lobbies are read-only. Pick a game first.")
		return false
	}
	inst.OnBlockPlace = func(c *ClientConnection, _ world.Position, _ world.Block) bool {
		_ = c.sendSystemMessage("Lobbies are read-only. Pick a game first.")
		return false
	}
	inst.OnPlayerAttack = func(_, _ *ClientConnection) bool { return false }

	prevJoin := inst.OnPlayerJoin
	inst.OnPlayerJoin = func(c *ClientConnection) {
		if prevJoin != nil {
			prevJoin(c)
		}
		giveBlazeRod(c)
		giveArenaSelector(c)
	}
}
