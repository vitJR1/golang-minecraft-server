package server

import (
	"bytes"
	"minecraft-server/game"
	"minecraft-server/protocol"
	"sort"
	"strings"
)

// Suggestions computes tab-completion replies for `text` (the full current
// chat-box text, e.g. "/op _LD"). Returns the position in text that the
// match strings replace, plus the matches themselves.
//
// Logic:
//   - First word (right after /): suggest command names.
//   - Argument after /op /deop /tp /gamemode (player slot): suggest online
//     player names.
//   - Argument after /instance: suggest instance IDs.
//   - Everything else: no suggestions.
//
// Matches always replace the word the cursor is on (last whitespace-
// delimited token). When the user just typed a trailing space, the
// "current word" is empty, so we list everything.
func (s *Server) Suggestions(c *ClientConnection, text string) (start, length int32, matches []string) {
	// Strip leading slash, remember offset for the wire `start` field.
	raw := text
	var offset int32
	if strings.HasPrefix(raw, "/") {
		raw = raw[1:]
		offset = 1
	}

	// Find the current word being completed (chars after the last space).
	lastSpace := strings.LastIndex(raw, " ")
	var prefix string
	var wordStart int32
	if lastSpace < 0 {
		prefix = raw
		wordStart = offset
	} else {
		prefix = raw[lastSpace+1:]
		wordStart = offset + int32(lastSpace) + 1
	}

	// Tokenize what's been typed so far to figure out which slot we're in.
	parts := strings.Fields(raw)
	trailingSpace := strings.HasSuffix(raw, " ")

	var cmd string
	var argIdx int
	switch {
	case len(parts) == 0:
		// Nothing typed — list all commands.
		argIdx = 0
	case trailingSpace:
		cmd = strings.ToLower(parts[0])
		argIdx = len(parts) // about to type the next arg
	default:
		cmd = strings.ToLower(parts[0])
		argIdx = len(parts) - 1
	}

	// Build the candidate set.
	var candidates []string
	switch {
	case argIdx == 0:
		// Command name slot. Same op-filter as commandsVisibleTo so
		// non-ops don't see /ban /op /tp etc. in autocomplete.
		isOp := s.Ops.Has(c.playerName)
		seen := map[*Command]bool{}
		for name, command := range commandRegistry {
			if seen[command] {
				continue
			}
			seen[command] = true
			if command.NeedsOp && !isOp {
				continue
			}
			candidates = append(candidates, name)
		}
	case takesPlayerName(cmd, argIdx):
		candidates = s.PlayerNames()
	case cmd == "template" || cmd == "templates":
		if argIdx == 1 {
			candidates = []string{"list"}
		}
	case cmd == "instance" || cmd == "i":
		switch argIdx {
		case 1:
			candidates = []string{"create", "join", "delete", "list", "set"}
		case 2:
			// join/delete take an instance ID; set takes a property name.
			// create takes a new id (no suggestion), list takes no args.
			if len(parts) >= 2 {
				switch strings.ToLower(parts[1]) {
				case "join", "go", "delete", "remove", "rm":
					candidates = s.InstanceIDs()
				case "set":
					candidates = []string{"pvp", "instantrespawn"}
				}
			}
		case 3:
			if len(parts) >= 2 {
				switch strings.ToLower(parts[1]) {
				case "create", "new":
					// /instance create <id> [template] — template name.
					candidates = s.TemplateNames()
				case "set":
					// /instance set <prop> <on|off> — the value.
					candidates = []string{"on", "off"}
				}
			}
		case 4:
			// /instance set <prop> <on|off> [id] — target instance.
			if len(parts) >= 2 && strings.ToLower(parts[1]) == "set" {
				candidates = s.InstanceIDs()
			}
		}
	case cmd == "arena":
		switch argIdx {
		case 1:
			candidates = []string{"create", "list"}
		case 2:
			// create <game> / list <game> → arena-capable game kinds.
			candidates = game.ArenaKinds()
		case 3:
			// create <game> <template> → template names.
			if len(parts) >= 2 && strings.EqualFold(parts[1], "create") {
				candidates = s.TemplateNames()
			}
		}
	case cmd == "play" || cmd == "queue" || cmd == "q":
		switch argIdx {
		case 1:
			candidates = append([]string{"leave", "list", "status"}, s.baseGameIDs()...)
		case 2:
			// /play <game> <arena> → arenas of that kind.
			candidates = s.ArenasOfKind(strings.ToLower(parts[1]))
		}
	}

	// Filter by prefix (case-insensitive) and sort for stable UX.
	lowPrefix := strings.ToLower(prefix)
	for _, cand := range candidates {
		if strings.HasPrefix(strings.ToLower(cand), lowPrefix) {
			matches = append(matches, cand)
		}
	}
	sort.Strings(matches)
	return wordStart, int32(len(prefix)), matches
}

// takesPlayerName reports whether cmd's argIdx-th argument expects a
// player name. Keep in sync with the command implementations in
// commands.go.
//
// /unban is intentionally NOT here — banned players are offline and
// PlayerNames() only lists online connections, so the suggestion list
// would always be empty. A separate "banned names" source would be
// needed if we want unban autocomplete.
func takesPlayerName(cmd string, argIdx int) bool {
	switch cmd {
	case "op", "deop":
		return argIdx == 1 // /op <player>
	case "tp", "teleport":
		return argIdx == 1 // /tp <player> (also accepts coords; harmless to suggest)
	case "gamemode", "gm":
		return argIdx == 2 // /gamemode <mode> [player]
	case "ban", "kick", "mute", "unmute":
		return argIdx == 1 // /<cmd> <player> [args...]
	case "banip", "unbanip":
		return argIdx == 1 // /<cmd> <player|ip>; player names suggested
	}
	return false
}

// sendCommandSuggestionsResponse writes Cb 0x11. Per the 1.20.1 spec:
// VarInt(txID) + VarInt(start) + VarInt(length) + VarInt(count) +
// for each match: String(match) + Bool(hasTooltip [+ Chat tooltip]).
func (c *ClientConnection) sendCommandSuggestionsResponse(txID, start, length int32, matches []string) error {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, txID)
	protocol.WriteVarInt32ToBuffer(&buf, start)
	protocol.WriteVarInt32ToBuffer(&buf, length)
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(matches)))
	for _, m := range matches {
		buf.Write(protocol.WriteString(m))
		buf.WriteByte(0) // hasTooltip = false
	}
	return c.safeWrite(CbPlayCommandSuggestResp, buf.Bytes())
}
