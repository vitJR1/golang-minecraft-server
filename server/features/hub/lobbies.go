package hub

import (
	"log/slog"
	"minecraft-server/server"
	"minecraft-server/server/instances/lobby"
	"minecraft-server/world"
)

// lobbies.go: the per-mode lobby instances. Their ids, map templates and
// which game returns to which lobby are server/instances/lobby.

// SetupLobbies creates the FFA / BedWars / SkyWars holding-pen
// instances and registers them on s. Each is a read-only lobby
// (build/break/PvP vetoed). World comes from the registered .schem
// template if present (loaded earlier by loadTemplates in main),
// otherwise falls back to a bare 11×11 stone pad at y=66.
//
// Calls into SetupLobbies are idempotent: an instance with the same ID
// already registered is left alone (won't overwrite an existing one
// that a test may have prepared).
func SetupLobbies(s *server.Server) {
	for _, id := range lobby.IDs() {
		if s.GetInstance(id) != nil {
			continue
		}
		w := lobby.DefaultWorld()
		templateName := lobby.TemplateName(id)
		if tmpl := s.GetTemplate(templateName); tmpl != nil {
			w = tmpl.Instantiate()
			slog.Info("lobby ready", "id", id, "template", templateName)
		} else {
			slog.Info("lobby ready", "id", id, "template", "(default platform)")
		}
		inst := server.NewInstance(id, s, w)
		installLobbyProtection(inst)
		if id == lobby.BedWars {
			inst.FreezeTime(6000) // like its arenas: always noon
		}
		s.AddInstance(inst)
	}
}

// installLobbyProtection wires the standard hooks: no breaking, no
// placing, no PvP. Same shape as Setup's hub-protection block.
// Also installs OnPlayerJoin so every entry to the lobby restocks the
// hotbar with the navigator (slot 0) + the arena selector (slot 1).
// MovePlayer doesn't send Set Container Slot itself, so without this
// the player's hotbar would be empty after the Respawn.
func installLobbyProtection(inst *server.Instance) {
	inst.OnBlockBreak = func(c *server.ClientConnection, _ world.Position) bool {
		_ = c.SendMessage("Lobbies are read-only. Pick a game first.")
		return false
	}
	inst.OnBlockPlace = func(c *server.ClientConnection, _ world.Position, _ world.Block) bool {
		_ = c.SendMessage("Lobbies are read-only. Pick a game first.")
		return false
	}
	inst.OnPlayerAttack = func(_, _ *server.ClientConnection) bool { return false }

	prevJoin := inst.OnPlayerJoin
	inst.OnPlayerJoin = func(c *server.ClientConnection) {
		if prevJoin != nil {
			prevJoin(c)
		}
		giveNavigator(c)
		giveSelector(c)
	}
}
