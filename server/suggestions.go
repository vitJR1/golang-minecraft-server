package server

import (
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/command"
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
	return command.ArgFrom(n, func(c *ClientConnection) []string { return c.server.PlayerNames() })
}

// fixedArg completes argument n from a fixed list.
func fixedArg(n int, words ...string) func(c *ClientConnection, args []string) []string {
	return command.FixedArg[*ClientConnection](n, words...)
}

// completeSpectate: /spectate [arena]
func completeSpectate(c *ClientConnection, args []string) []string {
	if len(args) == 1 {
		return c.server.ArenasOfKind("")
	}
	return nil
}

// sendCommandSuggestionsResponse writes Cb 0x11 (command.SuggestionsPayload).
func (c *ClientConnection) sendCommandSuggestionsResponse(txID, start, length int32, matches []string) error {
	return c.safeWrite(packetid.CbPlayCommandSuggestResp, command.SuggestionsPayload(txID, start, length, matches))
}
