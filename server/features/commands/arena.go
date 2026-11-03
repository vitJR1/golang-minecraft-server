package commands

import (
	"fmt"
	"minecraft-server/server"
	"strings"
)

// --- /arena command ---------------------------------------------------------

func cmdArena(c *server.ClientConnection, args []string) {
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

func arenaUsage(c *server.ClientConnection) {
	_ = c.SendMessage("Usage:")
	_ = c.SendMessage("  /arena create <game> <template> [name]")
	_ = c.SendMessage("  /arena list [game]")
}

func init() {
	server.RegisterCommand(&server.Command{
		Name:     "arena",
		NeedsOp:  true,
		Help:     "/arena create <game> <template> [name] | /arena list [game]",
		Run:      cmdArena,
		Complete: completeArena,
	})
}

// cmdArenaCreate handles /arena create <game> <template> [name].
func cmdArenaCreate(c *server.ClientConnection, args []string) {
	if len(args) < 2 || len(args) > 3 {
		_ = c.SendMessage("Usage: /arena create <game> <template> [name]")
		return
	}
	kind := strings.ToLower(args[0])
	templateName := args[1]
	name := ""
	if len(args) == 3 {
		name = args[2]
	}

	created, err := c.Server().CreateArena(kind, templateName, name)
	if err != nil {
		_ = c.SendMessage("Arena create failed: " + err.Error())
		return
	}
	_ = c.SendMessage(fmt.Sprintf("Created arena %q — /play %s %s", created, kind, created))
}

func cmdArenaList(c *server.ClientConnection, args []string) {
	kind := ""
	if len(args) >= 1 {
		kind = strings.ToLower(args[0])
	}
	arenas := c.Server().ArenasOfKind(kind)
	if len(arenas) == 0 {
		_ = c.SendMessage("No arenas created yet.")
		return
	}
	_ = c.SendMessage(fmt.Sprintf("Arenas (%d):", len(arenas)))
	for _, a := range arenas {
		_ = c.SendMessage("  " + a)
	}
}
