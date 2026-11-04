package commands

import (
	"fmt"
	"sort"
	"strings"

	"minecraft-server/server"
	"minecraft-server/server/features/command"
	"minecraft-server/templates"
	"minecraft-server/world"
)

// cmd_instance.go: /instance and /template — instances and the map
// templates they are built from.

func init() {
	server.RegisterCommand(&server.Command{
		Name:     "instance",
		Aliases:  []string{"i"},
		NeedsOp:  true,
		Help:     "/instance <create|join|delete|list|set> [args]",
		Run:      cmdInstance,
		Complete: completeInstance,
	})
	server.RegisterCommand(&server.Command{
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
func cmdInstance(c *server.ClientConnection, args []string) {
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

func instanceUsage(c *server.ClientConnection) {
	_ = c.SendMessage("Usage:")
	_ = c.SendMessage("  /instance create <id> [template]")
	_ = c.SendMessage("  /instance join <id>")
	_ = c.SendMessage("  /instance delete <id>")
	_ = c.SendMessage("  /instance list")
	_ = c.SendMessage("  /instance set <pvp|instantrespawn> <on|off> [id]")
}

// cmdInstanceSet flips a runtime toggle on an instance. Defaults to the
// caller's current instance; an optional trailing id targets another one.
//
//	/instance set pvp on|off [id]
//	/instance set instantrespawn on|off [id]
func cmdInstanceSet(c *server.ClientConnection, args []string) {
	if len(args) < 2 {
		_ = c.SendMessage("Usage: /instance set <pvp|instantrespawn> <on|off> [id]")
		return
	}
	property := strings.ToLower(args[0])
	value, ok := command.ParseOnOff(args[1])
	if !ok {
		_ = c.SendMessage("Value must be on or off")
		return
	}

	inst := c.Instance()
	if len(args) >= 3 {
		inst = c.Server().GetInstance(args[2])
		if inst == nil {
			_ = c.SendMessage("Unknown instance: " + args[2])
			return
		}
	}
	if inst == nil {
		_ = c.SendMessage("Not in an instance")
		return
	}

	switch property {
	case "pvp":
		inst.SetPvP(value)
	case "instantrespawn", "respawn":
		inst.SetInstantRespawn(value)
	default:
		_ = c.SendMessage("Unknown property: " + property + " (pvp, instantrespawn)")
		return
	}
	_ = c.SendMessage(fmt.Sprintf("%s on instance %s is now %s",
		property, inst.ID, command.OnOff(value)))
}

func cmdInstanceCreate(c *server.ClientConnection, args []string) {
	if len(args) < 1 || len(args) > 2 || strings.TrimSpace(args[0]) == "" {
		_ = c.SendMessage("Usage: /instance create <id> [template]")
		return
	}
	id := args[0]
	if c.Server().GetInstance(id) != nil {
		_ = c.SendMessage("Instance already exists: " + id)
		return
	}
	var w world.World
	if len(args) == 2 {
		tmpl := c.Server().GetTemplate(args[1])
		if tmpl == nil {
			_ = c.SendMessage("Unknown template: " + args[1])
			return
		}
		w = tmpl.Instantiate()
	} else {
		w = world.NewMemoryWorld()
	}
	inst := server.NewGameInstance(id, c.Server(), w)
	c.Server().AddInstance(inst)
	msg := "Created instance " + id
	if len(args) == 2 {
		msg += " from template " + args[1]
	}
	_ = c.SendMessage(msg + " — /instance join " + id + " to enter")
}

func cmdInstanceJoin(c *server.ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.SendMessage("Usage: /instance join <id>")
		return
	}
	target := c.Server().GetInstance(args[0])
	if target == nil {
		_ = c.SendMessage("Unknown instance: " + args[0])
		return
	}
	if target == c.Instance() {
		_ = c.SendMessage("Already in " + target.ID)
		return
	}
	if err := c.Server().Join(c, target); err != nil {
		_ = c.SendMessage("Move failed: " + err.Error())
		return
	}
	_ = c.SendMessage("Joined " + target.ID)
}

func cmdInstanceDelete(c *server.ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.SendMessage("Usage: /instance delete <id>")
		return
	}
	id := args[0]
	if id == c.Server().Hub.ID {
		_ = c.SendMessage("Cannot delete hub")
		return
	}
	inst := c.Server().GetInstance(id)
	if inst == nil {
		_ = c.SendMessage("No such instance: " + id)
		return
	}

	// If the caller is the only one in there, evacuate them to hub first.
	// Moving someone else from this goroutine would race on their c.Instance().
	if inst.Players.Count() == 1 {
		only, ok := inst.Players.ByName(c.Name())
		if ok && only == c {
			if err := c.Server().Join(c, c.Server().Hub); err != nil {
				_ = c.SendMessage("Couldn't leave to hub: " + err.Error())
				return
			}
		}
	}

	if err := c.Server().RemoveInstance(id); err != nil {
		_ = c.SendMessage("Delete failed: " + err.Error())
		return
	}
	_ = c.SendMessage("Deleted instance " + id)
}

func cmdInstanceList(c *server.ClientConnection, args []string) {
	_ = args
	ids := c.Server().InstanceIDs()
	sort.Strings(ids)
	_ = c.SendMessage(fmt.Sprintf("Instances (%d):", len(ids)))
	for _, id := range ids {
		inst := c.Server().GetInstance(id)
		if inst == nil {
			continue
		}
		marker := ""
		if inst == c.Instance() {
			marker = " ← you are here"
		}
		_ = c.SendMessage(fmt.Sprintf("  %s — %d player(s)%s",
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
func cmdTemplate(c *server.ClientConnection, args []string) {
	if len(args) == 0 {
		_ = c.SendMessage("Usage: /template list")
		return
	}
	switch strings.ToLower(args[0]) {
	case "list", "ls":
		cmdTemplateList(c)
	default:
		_ = c.SendMessage("Usage: /template list")
	}
}

// cmdTemplateList walks templatesRoot recursively for *.schem files and
// reports their paths (relative to root, sans extension). Empty
// directory and missing-directory both return a friendly message
// rather than an error since they're normal states on a fresh checkout.
func cmdTemplateList(c *server.ClientConnection) {
	names, err := templates.Scan(templatesRoot)
	if err != nil {
		_ = c.SendMessage("Failed to scan " + templatesRoot + ": " + err.Error())
		return
	}
	if len(names) == 0 {
		_ = c.SendMessage("No .schem templates under " + templatesRoot + "/")
		return
	}
	_ = c.SendMessage(fmt.Sprintf("Templates (%d) in %s/:", len(names), templatesRoot))
	for _, n := range names {
		_ = c.SendMessage("  " + n)
	}
}
