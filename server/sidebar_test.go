package server

import (
	"bytes"
	"encoding/json"
	"testing"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// sentPacket is one queued outbound packet: id + body after the id.
type sentPacket struct {
	id   int32
	body []byte
}

// drainPackets empties c's outbound queue, returning every packet in order.
func drainPackets(t *testing.T, c *ClientConnection) []sentPacket {
	t.Helper()
	var out []sentPacket
	for len(c.outbound) > 0 {
		msg := <-c.outbound
		if len(msg.frame) == 0 {
			continue
		}
		buf := bytes.NewBuffer(msg.frame)
		if _, err := protocol.ReadVarInt(buf); err != nil {
			t.Fatal(err)
		}
		id, _ := protocol.ReadVarInt(buf)
		out = append(out, sentPacket{int32(id), buf.Bytes()})
	}
	return out
}

func countIDs(pkts []sentPacket, id int32) int {
	n := 0
	for _, p := range pkts {
		if p.id == id {
			n++
		}
	}
	return n
}

// teamPrefixOf parses an Update Teams body (mode 0 or 2) and returns the
// team name, mode and prefix text (the JSON component's text).
func teamPrefixOf(t *testing.T, body []byte) (team string, mode byte, prefix string) {
	t.Helper()
	buf := bytes.NewBuffer(body)
	team, _ = protocol.ReadStringFromBuf(buf)
	mode, _ = buf.ReadByte()
	if mode == teamModeRemove {
		return team, mode, ""
	}
	_, _ = protocol.ReadStringFromBuf(buf) // display name
	_, _ = buf.ReadByte()                  // friendly flags
	_, _ = protocol.ReadStringFromBuf(buf) // name tag visibility
	_, _ = protocol.ReadStringFromBuf(buf) // collision
	_, _ = protocol.ReadVarInt(buf)        // colour
	raw, _ := protocol.ReadStringFromBuf(buf)
	var comp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &comp); err != nil {
		t.Fatalf("prefix %q is not a text component: %v", raw, err)
	}
	return team, mode, comp.Text
}

func TestSidebarCreateUpdateClear(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Board", player.Survival, 0, 64, 0)

	c.setSidebar("§eBED WARS", []string{"Kills: 0", "", "Beds: 0"})
	pkts := drainPackets(t, c)
	// Objective create, one team + one score per line, then display.
	if countIDs(pkts, CbPlayUpdateObjectives) != 1 || countIDs(pkts, CbPlayUpdateTeams) != 3 ||
		countIDs(pkts, CbPlayUpdateScore) != 3 || countIDs(pkts, CbPlayDisplayObjective) != 1 {
		t.Fatalf("create packets: %+v", pkts)
	}
	if pkts[0].id != CbPlayUpdateObjectives || pkts[len(pkts)-1].id != CbPlayDisplayObjective {
		t.Error("objective must be created first and displayed last")
	}
	// Top line has the highest score; the holder is an invisible colour code.
	var top sentPacket
	for _, p := range pkts {
		if p.id == CbPlayUpdateScore {
			top = p
			break
		}
	}
	buf := bytes.NewBuffer(top.body)
	holder, _ := protocol.ReadStringFromBuf(buf)
	action, _ := protocol.ReadVarInt(buf)
	obj, _ := protocol.ReadStringFromBuf(buf)
	score, _ := protocol.ReadVarInt(buf)
	if holder != "§0" || action != 0 || obj != sidebarObjective || score != 3 {
		t.Errorf("first score: holder %q action %d obj %q score %d", holder, action, obj, score)
	}

	// Unchanged content → silence.
	c.setSidebar("§eBED WARS", []string{"Kills: 0", "", "Beds: 0"})
	if pkts := drainPackets(t, c); len(pkts) != 0 {
		t.Errorf("unchanged sidebar sent %d packets", len(pkts))
	}

	// One line changes → one team prefix update, nothing else.
	c.setSidebar("§eBED WARS", []string{"Kills: 1", "", "Beds: 0"})
	pkts = drainPackets(t, c)
	if len(pkts) != 1 || pkts[0].id != CbPlayUpdateTeams {
		t.Fatalf("line update packets: %+v", pkts)
	}
	if team, mode, prefix := teamPrefixOf(t, pkts[0].body); team != "sb_line_0" || mode != teamModeUpdate || prefix != "Kills: 1" {
		t.Errorf("line update: team %q mode %d prefix %q", team, mode, prefix)
	}

	// Growing by a line re-scores everything (positions shift) and creates
	// the new line's team.
	c.setSidebar("§eBED WARS", []string{"Kills: 1", "", "Beds: 0", "Team: Red"})
	pkts = drainPackets(t, c)
	if countIDs(pkts, CbPlayUpdateTeams) != 1 || countIDs(pkts, CbPlayUpdateScore) != 4 || countIDs(pkts, CbPlayUpdateObjectives) != 0 {
		t.Errorf("grow packets: %+v", pkts)
	}

	// Shrinking removes the dropped line's score and team.
	c.setSidebar("§eBED WARS", []string{"Kills: 1"})
	pkts = drainPackets(t, c)
	removedTeams := 0
	for _, p := range pkts {
		if p.id == CbPlayUpdateTeams {
			if _, mode, _ := teamPrefixOf(t, p.body); mode == teamModeRemove {
				removedTeams++
			}
		}
	}
	if removedTeams != 3 {
		t.Errorf("shrink removed %d teams, want 3", removedTeams)
	}

	// Title change → objective update (mode 2).
	c.setSidebar("§cBED WARS", []string{"Kills: 1"})
	pkts = drainPackets(t, c)
	if len(pkts) != 1 || pkts[0].id != CbPlayUpdateObjectives {
		t.Fatalf("title packets: %+v", pkts)
	}
	buf = bytes.NewBuffer(pkts[0].body)
	_, _ = protocol.ReadStringFromBuf(buf)
	if mode, _ := buf.ReadByte(); mode != objectiveUpdate {
		t.Errorf("title change mode %d, want update", mode)
	}

	// Clear: every team removed, then the objective; a second clear is silent.
	c.clearSidebar()
	pkts = drainPackets(t, c)
	if countIDs(pkts, CbPlayUpdateTeams) != 1 || pkts[len(pkts)-1].id != CbPlayUpdateObjectives {
		t.Errorf("clear packets: %+v", pkts)
	}
	c.clearSidebar()
	if pkts := drainPackets(t, c); len(pkts) != 0 {
		t.Error("clearing nothing must be silent")
	}
	// Showing again after a clear re-creates from scratch.
	c.setSidebar("x", []string{"a"})
	if pkts := drainPackets(t, c); countIDs(pkts, CbPlayDisplayObjective) != 1 {
		t.Error("re-show should display the objective again")
	}
}

func TestSidebarCapsAtFifteenLines(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	c := tntConn(inst, "Board", player.Survival, 0, 64, 0)
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	c.setSidebar("t", lines)
	if n := countIDs(drainPackets(t, c), CbPlayUpdateTeams); n != sidebarMaxLines {
		t.Errorf("%d line teams, want %d", n, sidebarMaxLines)
	}
}
