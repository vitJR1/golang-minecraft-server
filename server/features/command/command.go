// Package command is the slash-command engine, independent of who sends the
// commands: a Registry of Commands (names, aliases, operator-only ones,
// commands from plugins), resolving a typed line, tab completion — each
// command completes its own arguments — and the command tree the client
// gets (Declare Commands). It is generic over the sender S; the server
// instantiates it with its connection type, and the commands' bodies live
// with the server.
package command

import (
	"bytes"
	"minecraft-server/protocol"
	"sort"
	"strings"
	"unicode/utf16"
)

// Sender is who runs a command.
type Sender interface {
	// IsOp reports operator rights (the only permission level).
	IsOp() bool
}

// Command is one slash command.
type Command[S Sender] struct {
	// Name is the primary word after the slash; Aliases are alternatives
	// (matched case-insensitively).
	Name    string
	Aliases []string
	// NeedsOp restricts the command to operators.
	NeedsOp bool
	// Help is the one-line usage shown by /help.
	Help string
	// Run executes the command; args are the words after the name.
	Run func(s S, args []string)
	// Complete, if set, suggests the argument being typed: args are the
	// words after the name, the last one the word under the cursor ("" right
	// after a space). The registry keeps the ones starting with that word.
	Complete func(s S, args []string) []string
}

// Registry holds the built-in commands and reaches the plugins' ones
// through Plugins. Register before serving; lookups don't lock.
type Registry[S Sender] struct {
	byName map[string]*Command[S]
	// Plugins, if set, lists the plugins' commands (their Names and
	// Aliases are matched after the built-ins, which win a clash).
	Plugins func() []*Command[S]
}

// Register adds a built-in command under its name and aliases.
func (r *Registry[S]) Register(c *Command[S]) {
	if r.byName == nil {
		r.byName = map[string]*Command[S]{}
	}
	r.byName[strings.ToLower(c.Name)] = c
	for _, a := range c.Aliases {
		r.byName[strings.ToLower(a)] = c
	}
}

// Builtin reports whether name is a built-in command or alias.
func (r *Registry[S]) Builtin(name string) bool {
	_, ok := r.byName[strings.ToLower(name)]
	return ok
}

// Lookup resolves a command name: built-ins first, then plugin commands.
func (r *Registry[S]) Lookup(name string) (*Command[S], bool) {
	name = strings.ToLower(name)
	if c, ok := r.byName[name]; ok {
		return c, true
	}
	if r.Plugins == nil {
		return nil, false
	}
	for _, c := range r.Plugins() {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
		for _, a := range c.Aliases {
			if strings.EqualFold(a, name) {
				return c, true
			}
		}
	}
	return nil, false
}

// All lists every command once: the built-ins, then the plugins' ones a
// built-in doesn't shadow.
func (r *Registry[S]) All() []*Command[S] {
	seen := make(map[*Command[S]]bool, len(r.byName))
	out := make([]*Command[S], 0, len(r.byName))
	for _, c := range r.byName {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	if r.Plugins != nil {
		for _, c := range r.Plugins() {
			if !r.Builtin(c.Name) {
				out = append(out, c)
			}
		}
	}
	return out
}

// VisibleTo lists the commands s may run (operator-only ones hidden from
// non-operators).
func (r *Registry[S]) VisibleTo(s S) []*Command[S] {
	all := r.All()
	op := s.IsOp()
	out := all[:0]
	for _, c := range all {
		if !c.NeedsOp || op {
			out = append(out, c)
		}
	}
	return out
}

// Resolve splits a typed line ("name arg1 arg2", the slash already gone)
// and finds its command. A name starting with "/" is a command in its own
// right only for double-slash commands ("//set"); otherwise the slash is
// taken for one the client included. name is the word as resolved (lower
// case, without such a slash); ok is false for an empty line or an unknown
// command (name still set).
func (r *Registry[S]) Resolve(line string) (cmd *Command[S], name string, args []string, ok bool) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil, "", nil, false
	}
	name, args = strings.ToLower(parts[0]), parts[1:]
	cmd, ok = r.Lookup(name)
	if !ok && strings.HasPrefix(name, "/") {
		name = strings.TrimPrefix(name, "/")
		cmd, ok = r.Lookup(name)
	}
	return cmd, name, args, ok
}

// Suggest computes the tab-completion reply for text (the whole chat box,
// e.g. "/op _LD"): the offset in text that the matches replace, its length,
// and the matches. The first word completes to the commands s may run;
// later ones come from the command's Complete — only for a command s may
// run, so an op-only command's completer (instance ids, arena / bot names…)
// never answers a non-operator. Matches start with the word under the cursor
// (case-insensitive) and are sorted. start and length count UTF-16 code
// units, as the client's brigadier (Java strings) reads them, not bytes.
func (r *Registry[S]) Suggest(s S, text string) (start, length int32, matches []string) {
	raw := text
	var offset int32
	if strings.HasPrefix(raw, "/") {
		raw, offset = raw[1:], 1
	}
	// The word being completed: what follows the last space.
	prefix, wordStart := raw, offset
	if i := strings.LastIndex(raw, " "); i >= 0 {
		prefix, wordStart = raw[i+1:], offset+int32(i)+1
	}
	parts := strings.Fields(raw)
	trailingSpace := strings.HasSuffix(raw, " ")

	var candidates []string
	if len(parts) == 0 || (len(parts) == 1 && !trailingSpace) {
		for _, c := range r.VisibleTo(s) {
			candidates = append(candidates, c.Name)
		}
	} else if c, ok := r.Lookup(parts[0]); ok && c.Complete != nil && (!c.NeedsOp || s.IsOp()) {
		args := append([]string{}, parts[1:]...)
		if trailingSpace {
			args = append(args, "")
		}
		candidates = c.Complete(s, args)
	}

	low := strings.ToLower(prefix)
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), low) {
			matches = append(matches, c)
		}
	}
	sort.Strings(matches)
	return utf16Len(text[:wordStart]), utf16Len(prefix), matches
}

// utf16Len is the length of s in UTF-16 code units (a rune above U+FFFF
// takes two).
func utf16Len(s string) int32 {
	var n int32
	for _, r := range s {
		n += int32(utf16.RuneLen(r))
	}
	return n
}

// SuggestionsPayload builds Command Suggestions Response (Cb 0x11, 1.20.1):
// VarInt(txID) + VarInt(start) + VarInt(length) + VarInt(count) + for each
// match String(match) + Bool(hasTooltip = false).
func SuggestionsPayload(txID, start, length int32, matches []string) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, txID)
	protocol.WriteVarInt32ToBuffer(&buf, start)
	protocol.WriteVarInt32ToBuffer(&buf, length)
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(matches)))
	for _, m := range matches {
		buf.Write(protocol.WriteString(m))
		buf.WriteByte(0) // hasTooltip = false
	}
	return buf.Bytes()
}
