package scoreboard

import (
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
