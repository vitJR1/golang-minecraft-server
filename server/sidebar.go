package server

import (
	"bytes"
	"fmt"
	"sync"

	"minecraft-server/protocol"
)

// sidebar.go: the per-player scoreboard sidebar (the panel on the right).
//
// Vanilla has no "show these lines" packet; a sidebar is a scoreboard
// objective displayed in slot 1 whose score holders are the lines, sorted
// by score (highest on top). We use the usual server trick so a line can be
// re-worded without flicker and two lines may read the same: each line's
// holder is a unique invisible string ("§0".."§e"), one scoreboard team per
// line carries the visible text as its prefix, and the holder's score is its
// position. Updating a line is one Update Teams packet; the red score
// numbers on the right can't be hidden on 1.20.1 (the number format came
// in 1.20.3), so lines count down like every 1.8–1.20 server sidebar.
//
// The client's scoreboard survives our two-hop Respawn (handleRespawn
// carries it into the new level), so nothing is re-sent on resync. Creating
// an objective or team the client already has throws on the client and
// kicks it, which is why the state is tracked per connection and MovePlayer
// clears the sidebar before the next instance may set one.

const (
	sidebarObjective = "sidebar" // objective names are ≤16 chars
	sidebarMaxLines  = 15        // the client shows at most 15
	sidebarSlot      = 1         // Display Objective position: sidebar
	teamColorReset   = 21        // Update Teams "formatting": reset
)

// sidebarState is what the client currently shows.
type sidebarState struct {
	title string
	lines []string
}

// sidebarHolder is line i's invisible score holder (a bare colour code
// renders as nothing).
func sidebarHolder(i int) string { return "§" + string("0123456789abcdef"[i]) }

// sidebarTeam is line i's team name (≤16 chars).
func sidebarTeam(i int) string { return fmt.Sprintf("sb_line_%d", i) }

// setSidebar shows (or updates) the sidebar with title and lines, top
// first; only what changed goes to the client. More than sidebarMaxLines
// lines are cut.
func (c *ClientConnection) setSidebar(title string, lines []string) {
	if c.player == nil {
		return
	}
	if len(lines) > sidebarMaxLines {
		lines = lines[:sidebarMaxLines]
	}
	c.sidebarMu.Lock()
	defer c.sidebarMu.Unlock()
	prev := c.sidebar
	next := &sidebarState{title: title, lines: append([]string(nil), lines...)}
	if prev == nil {
		_ = c.safeWrite(CbPlayUpdateObjectives, objectivePayload(sidebarObjective, objectiveCreate, title))
		for i, line := range lines {
			_ = c.safeWrite(CbPlayUpdateTeams, teamCreatePayload(sidebarTeam(i), line, sidebarHolder(i)))
			_ = c.safeWrite(CbPlayUpdateScore, scorePayload(sidebarHolder(i), sidebarObjective, int32(len(lines)-i)))
		}
		_ = c.safeWrite(CbPlayDisplayObjective, displayObjectivePayload(sidebarSlot, sidebarObjective))
		c.sidebar = next
		return
	}
	if title != prev.title {
		_ = c.safeWrite(CbPlayUpdateObjectives, objectivePayload(sidebarObjective, objectiveUpdate, title))
	}
	rescore := len(lines) != len(prev.lines) // every position shifts
	for i, line := range lines {
		switch {
		case i >= len(prev.lines):
			_ = c.safeWrite(CbPlayUpdateTeams, teamCreatePayload(sidebarTeam(i), line, sidebarHolder(i)))
			_ = c.safeWrite(CbPlayUpdateScore, scorePayload(sidebarHolder(i), sidebarObjective, int32(len(lines)-i)))
			continue
		case line != prev.lines[i]:
			_ = c.safeWrite(CbPlayUpdateTeams, teamUpdatePayload(sidebarTeam(i), line))
		}
		if rescore {
			_ = c.safeWrite(CbPlayUpdateScore, scorePayload(sidebarHolder(i), sidebarObjective, int32(len(lines)-i)))
		}
	}
	for i := len(lines); i < len(prev.lines); i++ {
		_ = c.safeWrite(CbPlayUpdateScore, scoreRemovePayload(sidebarHolder(i), sidebarObjective))
		_ = c.safeWrite(CbPlayUpdateTeams, teamRemovePayload(sidebarTeam(i)))
	}
	c.sidebar = next
}

// clearSidebar takes the sidebar down (no-op when none is shown).
func (c *ClientConnection) clearSidebar() {
	c.sidebarMu.Lock()
	defer c.sidebarMu.Unlock()
	prev := c.sidebar
	if prev == nil {
		return
	}
	c.sidebar = nil
	if c.player == nil {
		return
	}
	for i := range prev.lines {
		_ = c.safeWrite(CbPlayUpdateTeams, teamRemovePayload(sidebarTeam(i)))
	}
	// Removing the objective drops its scores and the display slot.
	_ = c.safeWrite(CbPlayUpdateObjectives, objectivePayload(sidebarObjective, objectiveRemove, ""))
}

// --- wire payloads ---------------------------------------------------------

// Update Objectives (0x58) actions.
const (
	objectiveCreate byte = 0
	objectiveRemove byte = 1
	objectiveUpdate byte = 2
)

// objectivePayload builds Update Objectives: name, action, and for
// create/update the display text (Chat) + type 0 (integer).
func objectivePayload(name string, action byte, title string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(name))
	buf.WriteByte(action)
	if action != objectiveRemove {
		buf.Write(textComponentPayload(title))
		protocol.WriteVarInt32ToBuffer(&buf, 0) // type: integer
	}
	return buf.Bytes()
}

// displayObjectivePayload builds Display Objective (0x51): slot + name.
func displayObjectivePayload(slot byte, name string) []byte {
	var buf bytes.Buffer
	buf.WriteByte(slot)
	buf.Write(protocol.WriteString(name))
	return buf.Bytes()
}

// scorePayload builds Update Score (0x5B) action 0: holder, objective, value.
func scorePayload(holder, objective string, value int32) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(holder))
	protocol.WriteVarInt32ToBuffer(&buf, 0) // create / update
	buf.Write(protocol.WriteString(objective))
	protocol.WriteVarInt32ToBuffer(&buf, value)
	return buf.Bytes()
}

// scoreRemovePayload builds Update Score action 1: holder, objective.
func scoreRemovePayload(holder, objective string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(holder))
	protocol.WriteVarInt32ToBuffer(&buf, 1) // remove
	buf.Write(protocol.WriteString(objective))
	return buf.Bytes()
}

// Update Teams (0x5A) modes.
const (
	teamModeCreate byte = 0
	teamModeRemove byte = 1
	teamModeUpdate byte = 2
)

// writeTeamInfo appends the mode-0/2 body: display name, friendly flags,
// name-tag visibility, collision rule, colour, prefix, suffix.
func writeTeamInfo(buf *bytes.Buffer, team, prefix string) {
	buf.Write(textComponentPayload(team))
	buf.WriteByte(0) // friendly flags
	buf.Write(protocol.WriteString("always"))
	buf.Write(protocol.WriteString("always"))
	protocol.WriteVarInt32ToBuffer(buf, teamColorReset)
	buf.Write(textComponentPayload(prefix))
	buf.Write(textComponentPayload(""))
}

// teamCreatePayload builds Update Teams mode 0 with one member.
func teamCreatePayload(team, prefix, member string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(team))
	buf.WriteByte(teamModeCreate)
	writeTeamInfo(&buf, team, prefix)
	protocol.WriteVarInt32ToBuffer(&buf, 1)
	buf.Write(protocol.WriteString(member))
	return buf.Bytes()
}

// teamUpdatePayload builds Update Teams mode 2 (new prefix = line text).
func teamUpdatePayload(team, prefix string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(team))
	buf.WriteByte(teamModeUpdate)
	writeTeamInfo(&buf, team, prefix)
	return buf.Bytes()
}

// teamRemovePayload builds Update Teams mode 1.
func teamRemovePayload(team string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(team))
	buf.WriteByte(teamModeRemove)
	return buf.Bytes()
}

// sidebarMutex is embedded in ClientConnection; kept as a named type so
// the zero value is ready to use.
type sidebarMutex = sync.Mutex
