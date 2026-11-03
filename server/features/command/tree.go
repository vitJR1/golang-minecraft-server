package command

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
// command name. The subcommand-aware logic is Registry.Suggest with each
// command's Complete — e.g. /instance subcommands, player slots for /op.
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

// Tree is the Declare Commands payload for the given command names (one
// literal + one ask-server argument leaf each).
func Tree(names []string) []byte {
	var buf bytes.Buffer

	// node count = root + 2 nodes per command (literal + argument leaf)
	totalNodes := 1 + 2*len(names)
	protocol.WriteVarInt32ToBuffer(&buf, int32(totalNodes))

	// --- Node 0: root ---
	buf.WriteByte(nodeFlagRoot)
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(names)))
	for i := range names {
		// Literal nodes occupy odd indices: 1, 3, 5, ...
		protocol.WriteVarInt32ToBuffer(&buf, int32(1+2*i))
	}

	// --- For each command: literal (executable) → argument (ask_server) ---
	for i, name := range names {
		literalIdx := 1 + 2*i
		argIdx := literalIdx + 1
		_ = literalIdx // for clarity

		// Literal: executable so the bare "/op" submits; has one child
		// (the argument node) so "/op something" walks deeper.
		buf.WriteByte(nodeFlagLiteral | nodeFlagExecutable)
		protocol.WriteVarInt32ToBuffer(&buf, 1) // 1 child
		protocol.WriteVarInt32ToBuffer(&buf, int32(argIdx))
		buf.Write(protocol.WriteString(name))

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

	return buf.Bytes()
}
