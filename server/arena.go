package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"minecraft-server/game"
)

// arena.go wires runtime arena creation: /arena create takes a game kind, a
// loaded map template, and the template's sibling JSON config, hands them to
// the game's registered ArenaBuilder, and registers the result as a playable
// game Definition the matchmaker can queue (under the arena's name). The
// server tracks name → kind so /play <game> <arena> can validate.

// CreateArena builds and registers a playable arena of kind from templateName,
// reading the layout config at <TemplateDir>/<templateName>.json. An empty
// name auto-generates one ("bw-<n>" for bedwars, "<kind>-<n>" otherwise).
// Returns the final arena name.
func (s *Server) CreateArena(kind, templateName, name string) (string, error) {
	builder, ok := game.GetArenaBuilder(kind)
	if !ok {
		return "", fmt.Errorf("game %q has no arena builder", kind)
	}
	tmpl := s.GetTemplate(templateName)
	if tmpl == nil {
		return "", fmt.Errorf("unknown template %q", templateName)
	}

	configPath := filepath.Join(s.TemplateDir, templateName+".json")
	config, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read arena config %s: %w", configPath, err)
	}

	if name == "" {
		name = s.nextArenaName(kind)
	}
	if _, exists := game.GetDef(name); exists {
		return "", fmt.Errorf("name %q already in use", name)
	}

	def, err := builder(name, name, tmpl, config)
	if err != nil {
		return "", err
	}
	if err := game.TryRegister(def); err != nil {
		return "", err
	}

	s.mu.Lock()
	s.arenas[name] = kind
	s.mu.Unlock()
	return name, nil
}

// nextArenaName picks the next free auto name for a kind ("bw-1", "bw-2", …
// for bedwars).
func (s *Server) nextArenaName(kind string) string {
	prefix := kind
	if kind == "bedwars" {
		prefix = "bw"
	}
	for {
		name := fmt.Sprintf("%s-%d", prefix, s.arenaSerial.Add(1))
		if _, exists := game.GetDef(name); !exists {
			return name
		}
	}
}

// IsArena reports whether name is a created arena.
func (s *Server) IsArena(name string) bool {
	s.mu.RLock()
	_, ok := s.arenas[name]
	s.mu.RUnlock()
	return ok
}

// ArenasOfKind returns the names of created arenas for a kind, sorted. kind ""
// returns every arena.
func (s *Server) ArenasOfKind(kind string) []string {
	s.mu.RLock()
	var out []string
	for name, k := range s.arenas {
		if kind == "" || k == kind {
			out = append(out, name)
		}
	}
	s.mu.RUnlock()
	sort.Strings(out)
	return out
}

// baseGameIDs returns registered game IDs that are base games (not created
// arenas), sorted — for /play tab-completion.
func (s *Server) baseGameIDs() []string {
	var out []string
	for _, d := range game.All() {
		if !s.IsArena(d.ID) {
			out = append(out, d.ID)
		}
	}
	sort.Strings(out)
	return out
}

// --- /arena command ---------------------------------------------------------

func cmdArena(c *ClientConnection, args []string) {
	if len(args) == 0 {
		arenaUsage(c)
		return
	}
	switch strings.ToLower(args[0]) {
	case "create", "new":
		cmdArenaCreate(c, args[1:])
	case "list", "ls":
		cmdArenaList(c, args[1:])
	default:
		arenaUsage(c)
	}
}

func arenaUsage(c *ClientConnection) {
	_ = c.sendSystemMessage("Usage:")
	_ = c.sendSystemMessage("  /arena create <game> <template> [name]")
	_ = c.sendSystemMessage("  /arena list [game]")
}

// cmdArenaCreate handles /arena create <game> <template> [name].
func cmdArenaCreate(c *ClientConnection, args []string) {
	if len(args) < 2 || len(args) > 3 {
		_ = c.sendSystemMessage("Usage: /arena create <game> <template> [name]")
		return
	}
	kind := strings.ToLower(args[0])
	templateName := args[1]
	name := ""
	if len(args) == 3 {
		name = args[2]
	}

	created, err := c.server.CreateArena(kind, templateName, name)
	if err != nil {
		_ = c.sendSystemMessage("Arena create failed: " + err.Error())
		return
	}
	_ = c.sendSystemMessage(fmt.Sprintf("Created arena %q — /play %s %s", created, kind, created))
}

func cmdArenaList(c *ClientConnection, args []string) {
	kind := ""
	if len(args) >= 1 {
		kind = strings.ToLower(args[0])
	}
	arenas := c.server.ArenasOfKind(kind)
	if len(arenas) == 0 {
		_ = c.sendSystemMessage("No arenas created yet.")
		return
	}
	_ = c.sendSystemMessage(fmt.Sprintf("Arenas (%d):", len(arenas)))
	for _, a := range arenas {
		_ = c.sendSystemMessage("  " + a)
	}
}
