package server

import (
	"fmt"
	"sort"
	"strings"

	"minecraft-server/server/command"
	"minecraft-server/templates"
	"minecraft-server/world"
)

// cmd_instance.go: /instance and /template — instances and the map
// templates they are built from.

func init() {
	registerCommand(&Command{
		Name:     "instance",
		Aliases:  []string{"i"},
		NeedsOp:  true,
		Help:     "/instance <create|join|delete|list|set> [args]",
		Run:      cmdInstance,
		Complete: completeInstance,
	})
	registerCommand(&Command{
		Name:     "template",
		Aliases:  []string{"templates"},
		NeedsOp:  true,
		Help:     "/template list — list .schem files under schem/templates/",
		Run:      cmdTemplate,
		Complete: completeTemplate,
	})
}

// cmdInstance dispatches to the create/join/delete/list subcommands.
// Subcommand parsing is lowercase + alias-tolerant.
func cmdInstance(c *ClientConnection, args []string) {
	if len(args) == 0 {
		instanceUsage(c)
		return
	}
	sub := strings.ToLower(args[0])
	rest := args[1:]
	switch sub {
	case "create", "new":
		cmdInstanceCreate(c, rest)
	case "join", "go":
		cmdInstanceJoin(c, rest)
	case "delete", "remove", "rm":
		cmdInstanceDelete(c, rest)
	case "list", "ls":
		cmdInstanceList(c, rest)
	case "set":
		cmdInstanceSet(c, rest)
	default:
		instanceUsage(c)
	}
}

func instanceUsage(c *ClientConnection) {
	_ = c.sendSystemMessage("Usage:")
	_ = c.sendSystemMessage("  /instance create <id> [template]")
	_ = c.sendSystemMessage("  /instance join <id>")
	_ = c.sendSystemMessage("  /instance delete <id>")
	_ = c.sendSystemMessage("  /instance list")
	_ = c.sendSystemMessage("  /instance set <pvp|instantrespawn> <on|off> [id]")
}

// cmdInstanceSet flips a runtime toggle on an instance. Defaults to the
// caller's current instance; an optional trailing id targets another one.
//
//	/instance set pvp on|off [id]
//	/instance set instantrespawn on|off [id]
func cmdInstanceSet(c *ClientConnection, args []string) {
	if len(args) < 2 {
		_ = c.sendSystemMessage("Usage: /instance set <pvp|instantrespawn> <on|off> [id]")
		return
	}
	property := strings.ToLower(args[0])
	value, ok := command.ParseOnOff(args[1])
	if !ok {
		_ = c.sendSystemMessage("Value must be on or off")
		return
	}

	inst := c.instance
	if len(args) >= 3 {
		inst = c.server.GetInstance(args[2])
		if inst == nil {
			_ = c.sendSystemMessage("Unknown instance: " + args[2])
			return
		}
	}
	if inst == nil {
		_ = c.sendSystemMessage("Not in an instance")
		return
	}

	switch property {
	case "pvp":
		inst.SetPvP(value)
	case "instantrespawn", "respawn":
		inst.SetInstantRespawn(value)
	default:
		_ = c.sendSystemMessage("Unknown property: " + property + " (pvp, instantrespawn)")
		return
	}
	_ = c.sendSystemMessage(fmt.Sprintf("%s on instance %s is now %s",
		property, inst.ID, command.OnOff(value)))
}

func cmdInstanceCreate(c *ClientConnection, args []string) {
	if len(args) < 1 || len(args) > 2 || strings.TrimSpace(args[0]) == "" {
		_ = c.sendSystemMessage("Usage: /instance create <id> [template]")
		return
	}
	id := args[0]
	if c.server.GetInstance(id) != nil {
		_ = c.sendSystemMessage("Instance already exists: " + id)
		return
	}
	var w world.World
	if len(args) == 2 {
		tmpl := c.server.GetTemplate(args[1])
		if tmpl == nil {
			_ = c.sendSystemMessage("Unknown template: " + args[1])
			return
		}
		w = tmpl.Instantiate()
	} else {
		w = world.NewMemoryWorld()
	}
	inst := NewInstance(id, c.server, w)
	c.server.AddInstance(inst)
	msg := "Created instance " + id
	if len(args) == 2 {
		msg += " from template " + args[1]
	}
	_ = c.sendSystemMessage(msg + " — /instance join " + id + " to enter")
}

func cmdInstanceJoin(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /instance join <id>")
		return
	}
	target := c.server.GetInstance(args[0])
	if target == nil {
		_ = c.sendSystemMessage("Unknown instance: " + args[0])
		return
	}
	if target == c.instance {
		_ = c.sendSystemMessage("Already in " + target.ID)
		return
	}
	// Default spawn (0, 80, 0). Per-instance spawn config comes later.
	if err := c.server.MovePlayer(c, target, 0, 80, 0); err != nil {
		_ = c.sendSystemMessage("Move failed: " + err.Error())
		return
	}
	_ = c.sendSystemMessage("Joined " + target.ID)
}

func cmdInstanceDelete(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /instance delete <id>")
		return
	}
	id := args[0]
	if id == c.server.Hub.ID {
		_ = c.sendSystemMessage("Cannot delete hub")
		return
	}
	inst := c.server.GetInstance(id)
	if inst == nil {
		_ = c.sendSystemMessage("No such instance: " + id)
		return
	}

	// If the caller is the only one in there, evacuate them to hub first.
	// Moving someone else from this goroutine would race on their c.instance.
	if inst.Players.Count() == 1 {
		only, ok := inst.Players.ByName(c.playerName)
		if ok && only == c {
			if err := c.server.MovePlayer(c, c.server.Hub, 0, 80, 0); err != nil {
				_ = c.sendSystemMessage("Couldn't leave to hub: " + err.Error())
				return
			}
		}
	}

	if err := c.server.RemoveInstance(id); err != nil {
		_ = c.sendSystemMessage("Delete failed: " + err.Error())
		return
	}
	_ = c.sendSystemMessage("Deleted instance " + id)
}

func cmdInstanceList(c *ClientConnection, args []string) {
	_ = args
	ids := c.server.InstanceIDs()
	sort.Strings(ids)
	_ = c.sendSystemMessage(fmt.Sprintf("Instances (%d):", len(ids)))
	for _, id := range ids {
		inst := c.server.GetInstance(id)
		if inst == nil {
			continue
		}
		marker := ""
		if inst == c.instance {
			marker = " ← you are here"
		}
		_ = c.sendSystemMessage(fmt.Sprintf("  %s — %d player(s)%s",
			id, inst.Players.Count(), marker))
	}
}

// templatesRoot is where /template list scans. Kept as a var so tests
// or future config can point it elsewhere; production stays at the
// project-relative default that ships with the repo.
var templatesRoot = templates.Root

// cmdTemplate dispatches the /template subcommands. Only `list` exists
// today — there's room for `/template load <name>` once we wire that
// into the in-memory template registry.
func cmdTemplate(c *ClientConnection, args []string) {
	if len(args) == 0 {
		_ = c.sendSystemMessage("Usage: /template list")
		return
	}
	switch strings.ToLower(args[0]) {
	case "list", "ls":
		cmdTemplateList(c)
	default:
		_ = c.sendSystemMessage("Usage: /template list")
	}
}

// cmdTemplateList walks templatesRoot recursively for *.schem files and
// reports their paths (relative to root, sans extension). Empty
// directory and missing-directory both return a friendly message
// rather than an error since they're normal states on a fresh checkout.
func cmdTemplateList(c *ClientConnection) {
	names, err := templates.Scan(templatesRoot)
	if err != nil {
		_ = c.sendSystemMessage("Failed to scan " + templatesRoot + ": " + err.Error())
		return
	}
	if len(names) == 0 {
		_ = c.sendSystemMessage("No .schem templates under " + templatesRoot + "/")
		return
	}
	_ = c.sendSystemMessage(fmt.Sprintf("Templates (%d) in %s/:", len(names), templatesRoot))
	for _, n := range names {
		_ = c.sendSystemMessage("  " + n)
	}
}
