package commands

import (
	"minecraft-server/game"
	"minecraft-server/server"
	"minecraft-server/server/features/command"
	"minecraft-server/server/gameplay/effect"
	"strings"
)

// complete.go: tab completion of the built-in commands' arguments. The
// completers get the words typed after the command name, the last one the
// word under the cursor; argument n (1-based) is being typed when
// len(args) == n.

// playerArg completes online player names as argument n.
func playerArg(n int) func(c *server.ClientConnection, args []string) []string {
	return command.ArgFrom(n, func(c *server.ClientConnection) []string { return c.Server().PlayerNames() })
}

// fixedArg completes argument n from a fixed list.
func fixedArg(n int, words ...string) func(c *server.ClientConnection, args []string) []string {
	return command.FixedArg[*server.ClientConnection](n, words...)
}

// completeEffect: /effect <player> <effect|clear> …
func completeEffect(c *server.ClientConnection, args []string) []string {
	switch len(args) {
	case 1:
		return c.Server().PlayerNames()
	case 2:
		return append(effect.Names(), "clear")
	}
	return nil
}

// completeTemplate: /template list
func completeTemplate(c *server.ClientConnection, args []string) []string {
	return fixedArg(1, "list")(c, args)
}

// completeInstance: /instance create <id> [template] | join|delete <id> |
// list | set <pvp|instantrespawn> <on|off> [id]
func completeInstance(c *server.ClientConnection, args []string) []string {
	s := c.Server()
	if len(args) == 1 {
		return []string{"create", "join", "delete", "list", "set"}
	}
	sub := strings.ToLower(args[0])
	switch len(args) {
	case 2:
		// join/delete take an instance ID; set takes a property name.
		// create takes a new id (no suggestion), list takes no args.
		switch sub {
		case "join", "go", "delete", "remove", "rm":
			return s.InstanceIDs()
		case "set":
			return []string{"pvp", "instantrespawn"}
		}
	case 3:
		switch sub {
		case "create", "new":
			return s.TemplateNames() // /instance create <id> [template]
		case "set":
			return []string{"on", "off"} // /instance set <prop> <on|off>
		}
	case 4:
		if sub == "set" {
			return s.InstanceIDs() // /instance set <prop> <on|off> [id]
		}
	}
	return nil
}

// completeArena: /arena create <game> <template> [name] | list [game]
func completeArena(c *server.ClientConnection, args []string) []string {
	switch len(args) {
	case 1:
		return []string{"create", "list"}
	case 2:
		return game.ArenaKinds() // arena-capable game kinds
	case 3:
		if strings.EqualFold(args[0], "create") {
			return c.Server().TemplateNames()
		}
	}
	return nil
}

// completePlay: /play <game> [arena] | leave | list | status
func completePlay(c *server.ClientConnection, args []string) []string {
	switch len(args) {
	case 1:
		return append([]string{"leave", "list", "status"}, c.Server().BaseGameIDs()...)
	case 2:
		return c.Server().ArenasOfKind(strings.ToLower(args[0])) // arenas of that kind
	}
	return nil
}

// completeSpectate: /spectate [arena]
func completeSpectate(c *server.ClientConnection, args []string) []string {
	if len(args) == 1 {
		return c.Server().ArenasOfKind("")
	}
	return nil
}
