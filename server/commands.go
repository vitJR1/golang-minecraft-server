package server

import (
	"fmt"
	"io/fs"
	"minecraft-server/ban"
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
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
		Help:    "/instance <create|join|delete|list|set> [args]",
		Run:     cmdInstance,
	})
	registerCommand(&Command{
		Name:    "template",
		Aliases: []string{"templates"},
		NeedsOp: true,
		Help:    "/template list — list .schem files under schem/templates/",
		Run:     cmdTemplate,
	})
	registerCommand(&Command{
		Name:    "play",
		Aliases: []string{"queue", "q"},
		NeedsOp: false,
		Help:    "/play <game> [arena] | /play leave | /play list — matchmaker",
		Run:     cmdPlay,
	})
	registerCommand(&Command{
		Name:    "arena",
		NeedsOp: true,
		Help:    "/arena create <game> <template> [name] | /arena list [game]",
		Run:     cmdArena,
	})
	registerCommand(&Command{
		Name:    "hub",
		Aliases: []string{"lobby"},
		NeedsOp: false,
		Help:    "/hub — teleport back to the hub instance",
		Run:     cmdHub,
	})
	registerCommand(&Command{
		Name:    "ban",
		NeedsOp: true,
		Help:    "/ban <player> <duration> [reason] — 1m, 7d, 2h, 1w",
		Run:     cmdBan,
	})
	registerCommand(&Command{
		Name:    "unban",
		NeedsOp: true,
		Help:    "/unban <player>",
		Run:     cmdUnban,
	})
	registerCommand(&Command{
		Name:    "kick",
		NeedsOp: true,
		Help:    "/kick <player> [reason]",
		Run:     cmdKick,
	})
	registerCommand(&Command{
		Name:    "mute",
		NeedsOp: true,
		Help:    "/mute <player> <duration> — 30s, 5m, 1h, 7d",
		Run:     cmdMute,
	})
	registerCommand(&Command{
		Name:    "unmute",
		NeedsOp: true,
		Help:    "/unmute <player>",
		Run:     cmdUnmute,
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
	// Auth gate: while unauthed only the whitelist (register/login/
	// hub/help) is dispatchable. Keeps un-authed players from running
	// /play, /tp etc. before they prove who they are.
	if authStore != nil && !c.authed.Load() {
		if _, allowed := authBypassCommands[name]; !allowed {
			_ = c.sendSystemMessage("Please /login or /register first.")
			return
		}
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
	value, ok := parseOnOff(args[1])
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
		property, inst.ID, onOff(value)))
}

// parseOnOff accepts the usual truthy/falsy words for a boolean argument.
func parseOnOff(s string) (value, ok bool) {
	switch strings.ToLower(s) {
	case "on", "true", "yes", "1", "enable", "enabled":
		return true, true
	case "off", "false", "no", "0", "disable", "disabled":
		return false, true
	default:
		return false, false
	}
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
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

// --- /play ---

// cmdPlay routes the matchmaker subcommands.
//
//	/play <gameID>   — join the queue for that game
//	/play leave      — leave whichever queue you're in
//	/play list       — show registered games + queue sizes
//	/play status     — show what queue you're currently in (if any)
func cmdPlay(c *ClientConnection, args []string) {
	if len(args) == 0 {
		_ = c.sendSystemMessage("Usage: /play <game> | /play leave | /play list | /play status")
		return
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "leave", "cancel":
		c.server.Matchmaker.Dequeue(c)
		_ = c.sendSystemMessage("Left matchmaker queue")
	case "list", "ls":
		// Base games only; created arenas are listed via /arena list.
		var defs []*game.Definition
		for _, d := range game.All() {
			if !c.server.IsArena(d.ID) {
				defs = append(defs, d)
			}
		}
		if len(defs) == 0 {
			_ = c.sendSystemMessage("No games registered.")
			return
		}
		sort.Slice(defs, func(i, j int) bool { return defs[i].ID < defs[j].ID })
		_ = c.sendSystemMessage(fmt.Sprintf("Registered games (%d):", len(defs)))
		for _, d := range defs {
			_ = c.sendSystemMessage(fmt.Sprintf("  %s (%s) — %d/%d waiting, needs %d to start",
				d.ID, d.Name, c.server.Matchmaker.QueueSize(d.ID), d.MaxPlayers, d.MinPlayers))
		}
	case "status":
		if gameID, ok := c.server.Matchmaker.PlayerQueue(c); ok {
			_ = c.sendSystemMessage("Queued for " + gameID +
				" (" + strconv.Itoa(c.server.Matchmaker.QueueSize(gameID)) + " waiting)")
		} else {
			_ = c.sendSystemMessage("Not in any matchmaker queue")
		}
	default:
		// /play <game> [arena]: with an arena name, queue that specific arena
		// (its Definition is registered under the arena name); otherwise queue
		// the base game.
		gameID := sub
		if len(args) >= 2 {
			arena := args[1]
			if !c.server.IsArena(arena) {
				_ = c.sendSystemMessage("Unknown arena: " + arena + " (see /arena list " + sub + ")")
				return
			}
			gameID = arena
		}
		if err := c.server.Matchmaker.Queue(c, gameID); err != nil {
			_ = c.sendSystemMessage("Queue failed: " + err.Error())
			return
		}
		_ = c.sendSystemMessage("Queued for " + gameID +
			" (" + strconv.Itoa(c.server.Matchmaker.QueueSize(gameID)) + " waiting)")
	}
}

// --- /hub ---

// cmdHub is the only fixed-destination teleport command — every player
// can run it (no NeedsOp) since games need an "escape" back to the
// lobby. From hub itself it's a polite no-op.
//
// Default spawn (0.5, 67, 0.5) mirrors player.New's defaults so the
// player lands on top of the spawn schematic in the centre column.
func cmdHub(c *ClientConnection, args []string) {
	_ = args
	// Always drop any matchmaker queue — explicitly choosing "/hub" is
	// the user saying "I don't want a game right now", whether they're
	// in the hub already or coming back from an arena.
	c.server.Matchmaker.Dequeue(c)

	if c.instance == c.server.Hub {
		_ = c.sendSystemMessage("You're already in the hub.")
		return
	}
	if err := c.server.MovePlayer(c, c.server.Hub, 0.5, 67, 0.5); err != nil {
		_ = c.sendSystemMessage("Hub teleport failed: " + err.Error())
		return
	}
}

// --- /template ---

// templatesRoot is where /template list scans. Kept as a var so tests
// or future config can point it elsewhere; production stays at the
// project-relative default that ships with the repo.
var templatesRoot = "schem/templates"

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
	names, err := scanTemplates(templatesRoot)
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

// scanTemplates returns every *.schem path under root, relative to root
// and with the .schem extension stripped. Sorted alphabetically. A
// missing root returns (nil, nil) — equivalent to "no templates".
func scanTemplates(root string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Surface the root-missing case as "no templates", not error.
			if path == root {
				return filepath.SkipAll
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".schem") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		names = append(names, strings.TrimSuffix(rel, filepath.Ext(rel)))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// --- /ban /unban /kick /mute /unmute ---

// cmdBan: /ban <player> <duration> [reason words…]
//
// Adds an in-memory ban entry expiring at now+duration and, if the player
// is currently online, kicks them with a Play Disconnect carrying the
// reason. Duration uses our compact format: 30s / 5m / 2h / 7d / 1w.
//
// banlist.json on disk is left untouched — Load is read-only at boot.
// To persist, call ban.Save("banlist.json") from main; we don't auto-
// persist here to keep the moderator workflow predictable.
func cmdBan(c *ClientConnection, args []string) {
	if len(args) < 2 {
		_ = c.sendSystemMessage("Usage: /ban <player> <duration> [reason]")
		return
	}
	target := args[0]
	dur, err := ParseShortDuration(args[1])
	if err != nil {
		_ = c.sendSystemMessage("Bad duration: " + err.Error())
		return
	}
	reason := "banned by " + c.playerName
	if len(args) > 2 {
		reason = strings.Join(args[2:], " ")
	}
	until := time.Now().Add(dur)
	ban.Add(target, reason, until)

	_ = c.sendSystemMessage(fmt.Sprintf("Banned %s until %s — %s",
		target, until.Format("2006-01-02 15:04:05"), reason))

	if conn, _, ok := c.server.FindPlayer(target); ok {
		_ = conn.sendPlayDisconnect(fmt.Sprintf("You are banned: %s (until %s)",
			reason, until.Format("2006-01-02 15:04:05")))
		go conn.cleanup()
	}
}

func cmdUnban(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /unban <player>")
		return
	}
	ban.Remove(args[0])
	_ = c.sendSystemMessage("Unbanned " + args[0])
}

// cmdKick: /kick <player> [reason words…]
//
// Closes the target's connection with a Play Disconnect packet so the
// vanilla client shows the reason on the disconnect screen. No ban entry
// is created — they can rejoin immediately.
func cmdKick(c *ClientConnection, args []string) {
	if len(args) < 1 {
		_ = c.sendSystemMessage("Usage: /kick <player> [reason]")
		return
	}
	target := args[0]
	reason := "kicked by " + c.playerName
	if len(args) > 1 {
		reason = strings.Join(args[1:], " ")
	}
	conn, _, ok := c.server.FindPlayer(target)
	if !ok {
		_ = c.sendSystemMessage("Player not found: " + target)
		return
	}
	_ = conn.sendPlayDisconnect("Kicked: " + reason)
	_ = c.sendSystemMessage(fmt.Sprintf("Kicked %s — %s", target, reason))
	go conn.cleanup()
}

// cmdMute: /mute <player> <duration>
//
// Suppresses Sb Chat Message broadcasts from the target until expiry.
// Commands (Sb Chat Command) are NOT affected — mods can still see and
// respond to /msg / /report / etc. Adjust the chat handler if you want
// to silence those too.
func cmdMute(c *ClientConnection, args []string) {
	if len(args) != 2 {
		_ = c.sendSystemMessage("Usage: /mute <player> <duration>")
		return
	}
	target := args[0]
	dur, err := ParseShortDuration(args[1])
	if err != nil {
		_ = c.sendSystemMessage("Bad duration: " + err.Error())
		return
	}
	until := time.Now().Add(dur)
	c.server.Mutes.Mute(target, until)
	_ = c.sendSystemMessage(fmt.Sprintf("Muted %s until %s",
		target, until.Format("2006-01-02 15:04:05")))

	if conn, _, ok := c.server.FindPlayer(target); ok {
		_ = conn.sendSystemMessage(fmt.Sprintf("You have been muted until %s",
			until.Format("2006-01-02 15:04:05")))
	}
}

func cmdUnmute(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /unmute <player>")
		return
	}
	c.server.Mutes.Unmute(args[0])
	_ = c.sendSystemMessage("Unmuted " + args[0])
	if conn, _, ok := c.server.FindPlayer(args[0]); ok {
		_ = conn.sendSystemMessage("You have been unmuted")
	}
}

// --- /help ---

func cmdHelp(c *ClientConnection, args []string) {
	_ = args
	_ = c.sendSystemMessage("Available commands:")
	// Dedup by canonical name (aliases share *Command pointers).
	seen := map[*Command]bool{}
	hasOp := c.server.Ops.Has(c.playerName)
	for _, cmd := range commandRegistry {
		if seen[cmd] || (!hasOp && cmd.NeedsOp) {
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
