package server

import "minecraft-server/server/command"

// Command is a slash command run by a player connection (command.Command).
type Command = command.Command[*ClientConnection]

// commands holds the built-in commands, registered from init() next to their
// handlers, and reaches the plugins' ones (game.RegisterCommand) through
// pluginCommands (plugins.go); built-ins win a name clash.
var commands command.Registry[*ClientConnection]

func registerCommand(cmd *Command) { commands.Register(cmd) }

// IsOp reports whether the player is an operator (command.Sender).
func (c *ClientConnection) IsOp() bool {
	return c != nil && c.server != nil && c.server.Ops.Has(c.playerName)
}

func init() {
	commands.Plugins = pluginCommands
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
	cmd, name, args, ok := commands.Resolve(raw)
	if name == "" {
		return
	}
	if !ok {
		_ = c.sendSystemMessage("Unknown command: /" + name)
		return
	}
	// Auth gate: while unauthed only the whitelist (register/login/
	// hub/help) is dispatchable. Keeps un-authed players from running
	// /play, /tp etc. before they prove who they are.
	if g := s.authGate; g != nil && !c.authed.Load() {
		if !g.Bypass(name) {
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

// --- /help ---

func cmdHelp(c *ClientConnection, args []string) {
	_ = args
	_ = c.sendSystemMessage("Available commands:")
	for _, cmd := range commandsVisibleTo(c) {
		_ = c.sendSystemMessage("  " + cmd.Help)
	}
}
