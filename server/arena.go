package server

import (
	"fmt"
	"os"
	"sort"
	"time"

	"minecraft-server/game"
	"minecraft-server/templates"
)

// arena.go wires runtime arena creation: /arena create takes a game kind, a
// loaded map template, and the template's sibling JSON config, hands them to
// the game's registered ArenaBuilder, and registers the result as a playable
// game Definition the matchmaker can queue (under the arena's name). The
// server tracks name → kind so /play <game> <arena> can validate.

// CreateArena builds an arena of kind from templateName (reading the layout
// config at <TemplateDir>/<templateName>.json) and spins up a running Instance
// for it right away — named after the arena — that players join directly via
// /play <kind> <arena> (or /instance join <arena>) and where they see each
// other. An empty name auto-generates one ("bw-<n>" for bedwars). Returns the
// final arena name.
func (s *Server) CreateArena(kind, templateName, name string) (string, error) {
	builder, ok := game.GetArenaBuilder(kind)
	if !ok {
		return "", fmt.Errorf("game %q has no arena builder", kind)
	}
	tmpl := s.GetTemplate(templateName)
	if tmpl == nil {
		return "", fmt.Errorf("unknown template %q", templateName)
	}

	configPath := templates.ConfigFile(s.TemplateDir, templateName)
	config, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read arena config %s: %w", configPath, err)
	}

	if name == "" {
		name = s.nextArenaName(kind)
	}
	if s.GetInstance(name) != nil {
		return "", fmt.Errorf("instance %q already exists", name)
	}

	def, err := builder(name, name, tmpl, config)
	if err != nil {
		return "", err
	}
	logic := def.New()
	if logic == nil {
		return "", fmt.Errorf("arena builder for %q returned nil logic", kind)
	}

	// Spin up the running instance now: one shared world per arena, so every
	// joiner lands in the same instance and is mutually visible. Ephemeral:
	// the janitor collects it if it sits empty (never joined, or abandoned).
	inst := NewGameInstance(name, s, def.Template.Instantiate())
	inst.GameID = kind
	markEphemeral(inst, time.Now())
	s.AddInstance(inst)
	s.AttachLogic(inst, logic)

	s.arenas.Add(name, kind)
	return name, nil
}

// nextArenaName picks the next free auto name for a kind ("bw-1", "bw-2", …
// for bedwars; "bw-1x1-1", … for the bedwars-1x1 duel kind).
func (s *Server) nextArenaName(kind string) string {
	return s.arenas.NextName(kind, func(name string) bool { return s.GetInstance(name) != nil })
}

// IsArena reports whether name is a created arena.
func (s *Server) IsArena(name string) bool { return s.arenas.Is(name) }

// ArenasOfKind returns the names of created arenas for a kind, sorted. kind ""
// returns every arena.
func (s *Server) ArenasOfKind(kind string) []string { return s.arenas.OfKind(kind) }

// BaseGameIDs returns registered game IDs that are base games (not created
// arenas), sorted — for /play tab-completion.
func (s *Server) BaseGameIDs() []string {
	var out []string
	for _, d := range game.All() {
		if !s.IsArena(d.ID) {
			out = append(out, d.ID)
		}
	}
	sort.Strings(out)
	return out
}
