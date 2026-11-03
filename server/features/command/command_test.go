package command

import (
	"bytes"
	"strings"
	"testing"

	"minecraft-server/protocol"
)

type sender struct{ op bool }

func (s sender) IsOp() bool { return s.op }

func registry(ran *[]string) *Registry[sender] {
	r := &Registry[sender]{}
	r.Register(&Command[sender]{Name: "tp", Aliases: []string{"Teleport"}, NeedsOp: true,
		Run: func(_ sender, args []string) { *ran = append(*ran, "tp "+strings.Join(args, " ")) },
		Complete: func(_ sender, args []string) []string {
			if len(args) == 1 {
				return []string{"Alice", "alex", "Bob"}
			}
			return nil
		}})
	r.Register(&Command[sender]{Name: "help",
		Run: func(sender, []string) { *ran = append(*ran, "help") }})
	r.Plugins = func() []*Command[sender] {
		return []*Command[sender]{
			{Name: "/set", Aliases: []string{"wset"}, Run: func(_ sender, a []string) { *ran = append(*ran, "set "+strings.Join(a, " ")) }},
			{Name: "help", Run: func(sender, []string) { *ran = append(*ran, "plugin help") }},
		}
	}
	return r
}

func TestResolve(t *testing.T) {
	var ran []string
	r := registry(&ran)
	for line, want := range map[string]string{
		"tp 1 2 3":      "tp 1 2 3",
		"TELEPORT x":    "tp x",
		"/set stone":    "set stone", // double-slash plugin command
		"wset dirt":     "set dirt",
		"/help":         "help", // a client-included slash
		"help":          "help", // the built-in wins over the plugin's
		"  tp   a   b ": "tp a b",
	} {
		ran = nil
		cmd, _, args, ok := r.Resolve(line)
		if !ok {
			t.Fatalf("%q: not found", line)
		}
		cmd.Run(sender{}, args)
		if len(ran) != 1 || ran[0] != want {
			t.Errorf("%q ran %v, want %q", line, ran, want)
		}
	}
	if _, name, _, ok := r.Resolve("/nope x"); ok || name != "nope" {
		t.Errorf("unknown: name %q ok %v", name, ok)
	}
	if _, name, _, ok := r.Resolve("   "); ok || name != "" {
		t.Errorf("empty: name %q ok %v", name, ok)
	}
}

func TestAllAndVisibility(t *testing.T) {
	var ran []string
	r := registry(&ran)
	names := func(cs []*Command[sender]) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return strings.Join(out, ",")
	}
	if got := len(r.All()); got != 3 { // tp (once, despite its alias), help, /set
		t.Errorf("All: %s", names(r.All()))
	}
	for _, c := range r.VisibleTo(sender{op: false}) {
		if c.NeedsOp {
			t.Errorf("non-op sees %s", c.Name)
		}
	}
	if got := len(r.VisibleTo(sender{op: true})); got != 3 {
		t.Errorf("op sees %d", got)
	}
}

func TestSuggest(t *testing.T) {
	var ran []string
	r := registry(&ran)
	for _, tc := range []struct {
		who           sender
		text          string
		start, length int32
		want          string
	}{
		{sender{op: true}, "/", 1, 0, "/set,help,tp"},
		{sender{op: false}, "/", 1, 0, "/set,help"}, // op-only names hidden
		{sender{op: true}, "/t", 1, 1, "tp"},
		{sender{op: true}, "/tp ", 4, 0, "Alice,Bob,alex"},
		{sender{op: true}, "/tp al", 4, 2, "Alice,alex"}, // case-insensitive prefix
		{sender{op: true}, "/teleport B", 10, 1, "Bob"},  // aliases complete too
		{sender{op: true}, "/tp Alice ", 10, 0, ""},
		{sender{op: true}, "/help ", 6, 0, ""}, // no Complete
		{sender{op: true}, "/nope ", 6, 0, ""},
	} {
		st, ln, m := r.Suggest(tc.who, tc.text)
		if st != tc.start || ln != tc.length || strings.Join(m, ",") != tc.want {
			t.Errorf("%q → %d %d %v, want %d %d %s", tc.text, st, ln, m, tc.start, tc.length, tc.want)
		}
	}
}

func TestTree(t *testing.T) {
	buf := bytes.NewBuffer(Tree([]string{"op", "tp"}))
	n, _ := protocol.ReadVarInt(buf)
	if n != 5 {
		t.Fatalf("nodes: %d, want root + 2 per command", n)
	}
	if flags, _ := buf.ReadByte(); flags != nodeFlagRoot {
		t.Fatalf("root flags %#x", flags)
	}
	kids, _ := protocol.ReadVarInt(buf)
	a, _ := protocol.ReadVarInt(buf)
	b, _ := protocol.ReadVarInt(buf)
	if kids != 2 || a != 1 || b != 3 {
		t.Fatalf("root children %d [%d %d]", kids, a, b)
	}
	if flags, _ := buf.ReadByte(); flags != nodeFlagLiteral|nodeFlagExecutable {
		t.Fatalf("literal flags %#x", flags)
	}
	protocol.ReadVarInt(buf) // 1 child
	protocol.ReadVarInt(buf) // → node 2
	if name, _ := protocol.ReadStringFromBuf(buf); name != "op" {
		t.Fatalf("first literal %q", name)
	}
}
