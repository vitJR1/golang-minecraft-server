package server

import (
	"strings"

	"minecraft-server/game"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/command"
	"minecraft-server/server/effect"
)

// Suggestions computes the tab-completion reply for text (the whole chat
// box, e.g. "/op _LD"): the offset in text the matches replace, its length,
// and the matches. The command names come from the registry, the arguments
// from each command's Complete (below, next to the commands they serve).
func (s *Server) Suggestions(c *ClientConnection, text string) (start, length int32, matches []string) {
	return commands.Suggest(c, text)
}

// The completers below get the words typed after the command name, the last
// one the word under the cursor; argument n (1-based) is being typed when
// len(args) == n.

// playerArg completes online player names as argument n.
//
// /unban doesn't use it — banned players are offline and PlayerNames only
// lists online connections, so the list would always be empty.
func playerArg(n int) func(c *ClientConnection, args []string) []string {
	return func(c *ClientConnection, args []string) []string {
		if len(args) == n {
			return c.server.PlayerNames()
		}
		return nil
	}
}

// fixedArg completes argument n from a fixed list.
func fixedArg(n int, words ...string) func(c *ClientConnection, args []string) []string {
	return func(c *ClientConnection, args []string) []string {
		if len(args) == n {
			return words
		}
		return nil
	}
}

// completeEffect: /effect <player> <effect|clear> …
func completeEffect(c *ClientConnection, args []string) []string {
	switch len(args) {
	case 1:
		return c.server.PlayerNames()
	case 2:
		return append(effect.Names(), "clear")
	}
	return nil
}

// completeTemplate: /template list
func completeTemplate(c *ClientConnection, args []string) []string {
	return fixedArg(1, "list")(c, args)
}

// completeSpectate: /spectate [arena]
func completeSpectate(c *ClientConnection, args []string) []string {
	if len(args) == 1 {
		return c.server.ArenasOfKind("")
	}
	return nil
}

// completeInstance: /instance create <id> [template] | join|delete <id> |
// list | set <pvp|instantrespawn> <on|off> [id]
func completeInstance(c *ClientConnection, args []string) []string {
	s := c.server
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
func completeArena(c *ClientConnection, args []string) []string {
	switch len(args) {
	case 1:
		return []string{"create", "list"}
	case 2:
		return game.ArenaKinds() // arena-capable game kinds
	case 3:
		if strings.EqualFold(args[0], "create") {
			return c.server.TemplateNames()
		}
	}
	return nil
}

// completePlay: /play <game> [arena] | leave | list | status
func completePlay(c *ClientConnection, args []string) []string {
	switch len(args) {
	case 1:
		return append([]string{"leave", "list", "status"}, c.server.baseGameIDs()...)
	case 2:
		return c.server.ArenasOfKind(strings.ToLower(args[0])) // arenas of that kind
	}
	return nil
}

// sendCommandSuggestionsResponse writes Cb 0x11 (command.SuggestionsPayload).
func (c *ClientConnection) sendCommandSuggestionsResponse(txID, start, length int32, matches []string) error {
	return c.safeWrite(packetid.CbPlayCommandSuggestResp, command.SuggestionsPayload(txID, start, length, matches))
}
