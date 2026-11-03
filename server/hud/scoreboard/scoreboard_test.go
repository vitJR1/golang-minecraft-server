package scoreboard

import (
	"bytes"
	"testing"

	"minecraft-server/protocol/packetid"
)

func ids(pkts []Packet) map[int32]int {
	out := map[int32]int{}
	for _, p := range pkts {
		out[p.ID]++
	}
	return out
}

func TestSetSidebarSendsOnlyChanges(t *testing.T) {
	s, pkts := SetSidebar(nil, "T", []string{"a", "b"})
	n := ids(pkts)
	if n[packetid.CbPlayUpdateObjectives] != 1 || n[packetid.CbPlayUpdateTeams] != 2 ||
		n[packetid.CbPlayUpdateScore] != 2 || n[packetid.CbPlayDisplayObjective] != 1 {
		t.Fatalf("first show: %v", n)
	}
	s, pkts = SetSidebar(s, "T", []string{"a", "c"})
	if len(pkts) != 1 || pkts[0].ID != packetid.CbPlayUpdateTeams {
		t.Fatalf("one changed line: %d packets", len(pkts))
	}
	if _, pkts = SetSidebar(s, "T", []string{"a", "c"}); len(pkts) != 0 {
		t.Fatalf("no change: %d packets", len(pkts))
	}
	s, _ = SetSidebar(s, "T", []string{"a"})
	if len(s.Lines()) != 1 {
		t.Fatalf("lines %v", s.Lines())
	}
	if n := ids(ClearSidebar(s)); n[packetid.CbPlayUpdateTeams] != 1 || n[packetid.CbPlayUpdateObjectives] != 1 {
		t.Fatalf("clear: %v", n)
	}
	if ClearSidebar(nil) != nil {
		t.Fatal("clearing nothing sends nothing")
	}
	long := make([]string, 20)
	if s, _ := SetSidebar(nil, "T", long); len(s.Lines()) != SidebarMaxLines {
		t.Fatalf("cut to %d", len(s.Lines()))
	}
}

func TestSyncTags(t *testing.T) {
	want := Tags{}
	want.Add("red", "§c● ", 'c', "A")
	want.Add("a_group_name_that_is_long", "", 0, "B")
	if len(want) != 2 {
		t.Fatalf("groups %v", want)
	}
	for team := range want {
		if len(team) > 16 {
			t.Fatalf("team name %q over 16 characters", team)
		}
	}
	if got := len(SyncTags(nil, want)); got != 2 {
		t.Fatalf("create: %d packets", got)
	}
	if got := len(SyncTags(want, want)); got != 0 {
		t.Fatalf("in sync: %d packets", got)
	}
	next := Tags{}
	next.Add("red", "§c● ", 'c', "A")
	next.Add("red", "§c● ", 'c', "C")
	// red gains C; the long group goes.
	if got := len(SyncTags(want, next)); got != 2 {
		t.Fatalf("change: %d packets", got)
	}
	if ChatFormatting('c') != 12 || ChatFormatting(0) != colorReset {
		t.Fatal("ChatFormatting")
	}
}

// TestSyncTagsRemovalsFirst: a player moving between teams must be removed
// from the old team before any packet adds them to the new one — the vanilla
// client moves them on the add, and the later "remove from old" throws and
// kicks (review #23). The output is also deterministic.
func TestSyncTagsRemovalsFirst(t *testing.T) {
	have := Tags{}
	have.Add("a", "", 'c', "X")
	have.Add("a", "", 'c', "Y")
	have.Add("b", "", '9', "Z")
	have.Add("d", "", 'e', "V")
	want := Tags{}
	want.Add("a", "", 'c', "Y")
	want.Add("b", "", '9', "Z")
	want.Add("b", "", '9', "X") // X: a → b
	want.Add("c", "", 'a', "V") // V: d (gone) → c (new)
	want.Add("e", "", 'a', "W")

	first := SyncTags(have, want)
	for run := 0; run < 50; run++ {
		got := SyncTags(have, want)
		if len(got) != len(first) {
			t.Fatalf("run %d: %d packets, want %d", run, len(got), len(first))
		}
		for i := range got {
			if !bytes.Equal(got[i].Payload, first[i].Payload) {
				t.Fatalf("run %d: packet %d differs — output not deterministic", run, i)
			}
		}
		adding := false
		for i, p := range got {
			name := string(p.Payload[1 : 1+int(p.Payload[0])]) // team names are < 128 bytes
			switch mode := p.Payload[1+int(p.Payload[0])]; mode {
			case teamModeRemove, teamModeRemoveMembers:
				if adding {
					t.Fatalf("run %d: packet %d (team %s, mode %d) removes after an add/create", run, i, name, mode)
				}
			case teamModeCreate, teamModeAddMembers, teamModeUpdate:
				adding = true
			}
		}
	}
	// remove X from a, remove d, create c, add X to b, create e.
	if len(first) != 5 {
		t.Fatalf("%d packets, want 5", len(first))
	}
}
