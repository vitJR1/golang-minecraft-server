package server

import (
	"bytes"
	"minecraft-server/protocol"
)

// Declare Commands (Cb 0x10) sends the brigadier command tree to the
// client. Without it the 1.20.1 vanilla client greys out every / command
// as "unknown" and refuses to send it to the server. It's also what
// drives tab-complete dispatch: at each cursor position the client walks
// the tree to figure out which node it's in. Literal nodes complete
// locally (the client knows them); argument nodes only ask the server
// when they have a custom-suggestions tag set to "minecraft:ask_server".
//
// Tree shape we emit:
//
//	root
//	├── op       (literal, executable)
//	│   └── args (argument greedy-string, executable, suggestions=ask_server)
//	├── tp       (literal, executable)
//	│   └── args (…)
//	└── …
//
// One argument leaf per command is enough: with greedy-string the client
// asks the server for suggestions at every cursor position past the
// command name. The actual subcommand-aware logic lives server-side in
// suggestions.go (Server.Suggestions) — it parses the typed text and
// returns the right candidates for /instance subcommands, player slots
// for /op, etc.
//
// Node flag bits (1.20.1):
//
//	bits 0-1: 0=root, 1=literal, 2=argument
//	bit  2:   executable (command can be submitted at this node)
//	bit  3:   has redirect to another node
//	bit  4:   has custom suggestions identifier
const (
	nodeFlagRoot       = 0x00
	nodeFlagLiteral    = 0x01
	nodeFlagArgument   = 0x02
	nodeFlagExecutable = 0x04
	nodeFlagHasSuggest = 0x10
)

// Brigadier parser IDs (subset, full list on wiki.vg / minecraft.wiki).
const parserBrigadierString = 5

// brigadier:string mode VarInt.
const stringGreedyPhrase = 2

func (c *ClientConnection) sendDeclareCommands() error {
	cmds := commandsVisibleTo(c)

	var buf bytes.Buffer

	// node count = root + 2 nodes per command (literal + argument leaf)
	totalNodes := 1 + 2*len(cmds)
	protocol.WriteVarInt32ToBuffer(&buf, int32(totalNodes))

	// --- Node 0: root ---
	buf.WriteByte(nodeFlagRoot)
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(cmds)))
	for i := range cmds {
		// Literal nodes occupy odd indices: 1, 3, 5, ...
		protocol.WriteVarInt32ToBuffer(&buf, int32(1+2*i))
	}

	// --- For each command: literal (executable) → argument (ask_server) ---
	for i, cmd := range cmds {
		literalIdx := 1 + 2*i
		argIdx := literalIdx + 1
		_ = literalIdx // for clarity

		// Literal: executable so the bare "/op" submits; has one child
		// (the argument node) so "/op something" walks deeper.
		buf.WriteByte(nodeFlagLiteral | nodeFlagExecutable)
		protocol.WriteVarInt32ToBuffer(&buf, 1) // 1 child
		protocol.WriteVarInt32ToBuffer(&buf, int32(argIdx))
		buf.Write(protocol.WriteString(cmd.Name))

		// Argument leaf:
		//   - greedy brigadier:string: consumes the rest of the input
		//   - executable: command can be submitted from here too
		//   - has_suggestions: asks server every keystroke
		buf.WriteByte(nodeFlagArgument | nodeFlagExecutable | nodeFlagHasSuggest)
		protocol.WriteVarInt32ToBuffer(&buf, 0) // no children
		buf.Write(protocol.WriteString("args"))
		protocol.WriteVarInt32ToBuffer(&buf, parserBrigadierString)
		protocol.WriteVarInt32ToBuffer(&buf, stringGreedyPhrase) // parser data
		buf.Write(protocol.WriteString("minecraft:ask_server"))
	}

	// rootIndex
	protocol.WriteVarInt32ToBuffer(&buf, 0)

	return c.safeWrite(CbPlayDeclareCommands, buf.Bytes())
}

// uniqueRegisteredCommands returns each registered Command once, ignoring
// aliases (which share a *Command pointer in commandRegistry). Order is
// non-deterministic; callers that need stable order should sort by Name.
func uniqueRegisteredCommands() []*Command {
	seen := make(map[*Command]bool, len(commandRegistry))
	out := make([]*Command, 0, len(commandRegistry))
	for _, c := range commandRegistry {
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// commandsVisibleTo returns the subset of registered commands that c is
// allowed to see — op-only ones are hidden from non-ops so they don't
// appear in /<TAB> autocomplete or in the brigadier tree the client
// uses for syntax highlighting. Mirror the gate in RunCommand: if the
// command needs op and the player isn't op, drop it.
func commandsVisibleTo(c *ClientConnection) []*Command {
	all := uniqueRegisteredCommands()
	if c == nil || c.server == nil {
		return all
	}
	isOp := c.server.Ops.Has(c.playerName)
	out := make([]*Command, 0, len(all))
	for _, cmd := range all {
		if cmd.NeedsOp && !isOp {
			continue
		}
		out = append(out, cmd)
	}
	return out
}
