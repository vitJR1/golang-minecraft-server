package server

import (
	"fmt"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"sort"
	"strconv"
	"strings"
)

// Command is a slash-command implementation. The dispatcher routes by Name
// (case-insensitive). NeedsOp is the only permission level for now — a
// non-op trying to run an op command gets a "no permission" reply.
type Command struct {
	Name    string
	Aliases []string
	NeedsOp bool
	Help    string
	Run     func(c *ClientConnection, args []string)
}

// commandRegistry is filled in init(); RunCommand looks up by name and
// dispatches. A real plugin system would let games register their own
// commands — for now the set is fixed and small.
var commandRegistry = map[string]*Command{}

func registerCommand(cmd *Command) {
	commandRegistry[strings.ToLower(cmd.Name)] = cmd
	for _, alias := range cmd.Aliases {
		commandRegistry[strings.ToLower(alias)] = cmd
	}
}

func init() {
	registerCommand(&Command{
		Name:    "op",
		NeedsOp: true,
		Help:    "/op <player> — grant operator privileges",
		Run:     cmdOp,
	})
	registerCommand(&Command{
		Name:    "deop",
		NeedsOp: true,
		Help:    "/deop <player> — revoke operator privileges",
		Run:     cmdDeop,
	})
	registerCommand(&Command{
		Name:    "gamemode",
		Aliases: []string{"gm"},
		NeedsOp: true,
		Help:    "/gamemode <survival|creative|adventure|spectator> [player]",
		Run:     cmdGamemode,
	})
	registerCommand(&Command{
		Name:    "tp",
		Aliases: []string{"teleport"},
		NeedsOp: true,
		Help:    "/tp <player> | /tp <x> <y> <z>",
		Run:     cmdTp,
	})
	registerCommand(&Command{
		Name:    "instance",
		Aliases: []string{"i"},
		NeedsOp: true,
		Help:    "/instance <create|join|delete|list> [args]",
		Run:     cmdInstance,
	})
	registerCommand(&Command{
		Name:    "help",
		NeedsOp: false,
		Help:    "/help — list available commands",
		Run:     cmdHelp,
	})
}

// RunCommand parses a raw command string ("name arg1 arg2 ...") and
// dispatches. Validation of permission and arg count happens here so each
// command's Run body can assume a happy path.
func (s *Server) RunCommand(c *ClientConnection, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	parts := strings.Fields(raw)
	name := strings.ToLower(parts[0])
	args := parts[1:]

	cmd, ok := commandRegistry[name]
	if !ok {
		_ = c.sendSystemMessage("Unknown command: /" + name)
		return
	}
	if cmd.NeedsOp && !s.Ops.Has(c.playerName) {
		_ = c.sendSystemMessage("You don't have permission to use /" + name)
		return
	}
	cmd.Run(c, args)
}

// --- /op ---

func cmdOp(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /op <player>")
		return
	}
	target := args[0]
	c.server.Ops.Add(target)
	_ = c.sendSystemMessage("Granted op to " + target)
	if conn, _, ok := c.server.FindPlayer(target); ok {
		_ = conn.sendSystemMessage("You are now an operator")
	}
}

func cmdDeop(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /deop <player>")
		return
	}
	target := args[0]
	c.server.Ops.Remove(target)
	_ = c.sendSystemMessage("Revoked op from " + target)
	if conn, _, ok := c.server.FindPlayer(target); ok {
		_ = conn.sendSystemMessage("You are no longer an operator")
	}
}

// --- /gamemode ---

func cmdGamemode(c *ClientConnection, args []string) {
	if len(args) < 1 || len(args) > 2 {
		_ = c.sendSystemMessage("Usage: /gamemode <survival|creative|adventure|spectator> [player]")
		return
	}
	mode, ok := parseGamemode(args[0])
	if !ok {
		_ = c.sendSystemMessage("Unknown gamemode: " + args[0])
		return
	}
	target := c
	if len(args) == 2 {
		conn, _, ok := c.server.FindPlayer(args[1])
		if !ok {
			_ = c.sendSystemMessage("Player not found: " + args[1])
			return
		}
		target = conn
	}
	target.player.SetGamemode(mode)
	_ = target.sendGameModeChange(mode)
	_ = c.sendSystemMessage(fmt.Sprintf("Set %s's gamemode to %s", target.player.Name, args[0]))
	if target != c {
		_ = target.sendSystemMessage("Your gamemode is now " + args[0])
	}
}

func parseGamemode(s string) (player.Gamemode, bool) {
	switch strings.ToLower(s) {
	case "survival", "s", "0":
		return player.Survival, true
	case "creative", "c", "1":
		return player.Creative, true
	case "adventure", "a", "2":
		return player.Adventure, true
	case "spectator", "sp", "3":
		return player.Spectator, true
	}
	return 0, false
}

// --- /tp ---

func cmdTp(c *ClientConnection, args []string) {
	switch len(args) {
	case 1:
		// /tp <player> — teleport sender to target. Only works inside the
		// same instance for now — cross-instance teleport needs more wiring.
		target, inst, ok := c.server.FindPlayer(args[0])
		if !ok {
			_ = c.sendSystemMessage("Player not found: " + args[0])
			return
		}
		if inst != c.instance {
			_ = c.sendSystemMessage("Player is in another instance")
			return
		}
		s := target.player.Snapshot()
		teleportConnTo(c, s.X, s.Y, s.Z)
		_ = c.sendSystemMessage("Teleported to " + target.player.Name)
	case 3:
		// /tp <x> <y> <z>
		x, err := strconv.ParseFloat(args[0], 64)
		if err != nil {
			_ = c.sendSystemMessage("Bad X: " + args[0])
			return
		}
		y, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			_ = c.sendSystemMessage("Bad Y: " + args[1])
			return
		}
		z, err := strconv.ParseFloat(args[2], 64)
		if err != nil {
			_ = c.sendSystemMessage("Bad Z: " + args[2])
			return
		}
		teleportConnTo(c, x, y, z)
		_ = c.sendSystemMessage(fmt.Sprintf("Teleported to %.2f, %.2f, %.2f", x, y, z))
	default:
		_ = c.sendSystemMessage("Usage: /tp <player>  or  /tp <x> <y> <z>")
	}
}

// teleportConnTo updates the player's authoritative position and tells the
// client to repaint at the new coords. Other players see the new position
// via the standard entity-teleport broadcast on the next tick.
func teleportConnTo(c *ClientConnection, x, y, z float64) {
	c.player.MoveTo(x, y, z, false)
	// Teleport ID is arbitrary but should change; using nanos is plenty
	// unique within a session.
	teleportID := int32(1) // TODO: bump per-connection counter when we add server tick.
	_ = c.sendSyncPlayerPosition(x, y, z, teleportID)
	c.broadcastEntityTeleport()
}

// --- /instance ---

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

// --- /help ---

func cmdHelp(c *ClientConnection, args []string) {
	_ = args
	_ = c.sendSystemMessage("Available commands:")
	// Dedup by canonical name (aliases share *Command pointers).
	seen := map[*Command]bool{}
	for _, cmd := range commandRegistry {
		if seen[cmd] {
			continue
		}
		seen[cmd] = true
		_ = c.sendSystemMessage("  " + cmd.Help)
	}
}

// --- gamemode wire packet ---

// sendGameModeChange tells the client to switch gamemode. In 1.20.1 this
// is a Game Event packet (Cb 0x20) with event_id = 3 (Change Game Mode)
// and value = the mode index as a Float (the protocol stores it that way).
func (c *ClientConnection) sendGameModeChange(mode player.Gamemode) error {
	var payload []byte
	payload = append(payload, 3) // event id: Change Game Mode
	payload = append(payload, protocol.WriteFloat(float32(mode))...)
	return c.safeWrite(CbPlayGameEvent, payload)
}
