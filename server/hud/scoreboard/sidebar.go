package scoreboard

import (
	"fmt"

	"minecraft-server/protocol/packetid"
)

// The per-player sidebar (the panel on the right).
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
// MovePlayer clears the sidebar before the next instance may set one.

const (
	sidebarObjective = "sidebar" // objective names are ≤16 chars
	// SidebarMaxLines is the most lines the client shows; more are cut.
	SidebarMaxLines = 15
	sidebarSlot     = 1 // Display Objective position: sidebar
)

// Sidebar is what a client's sidebar shows; nil is none.
type Sidebar struct {
	title string
	lines []string
}

// Lines is the sidebar's lines, top first.
func (s *Sidebar) Lines() []string { return s.lines }

// sidebarHolder is line i's invisible score holder (a bare colour code
// renders as nothing).
func sidebarHolder(i int) string { return "§" + string("0123456789abcdef"[i]) }

// sidebarTeam is line i's team name (≤16 chars).
func sidebarTeam(i int) string { return fmt.Sprintf("sb_line_%d", i) }

// SetSidebar returns the sidebar showing title and lines (top first, cut at
// SidebarMaxLines) and the packets that turn prev (nil: none shown) into
// it — only what changed.
func SetSidebar(prev *Sidebar, title string, lines []string) (*Sidebar, []Packet) {
	if len(lines) > SidebarMaxLines {
		lines = lines[:SidebarMaxLines]
	}
	next := &Sidebar{title: title, lines: append([]string(nil), lines...)}
	var out []Packet
	score := func(i int) Packet {
		return Packet{packetid.CbPlayUpdateScore, scorePayload(sidebarHolder(i), sidebarObjective, int32(len(lines)-i))}
	}
	create := func(i int, line string) Packet {
		return teams(teamPayload(sidebarTeam(i), teamModeCreate, colorReset, line, []string{sidebarHolder(i)}))
	}
	if prev == nil {
		out = append(out, Packet{packetid.CbPlayUpdateObjectives, objectivePayload(sidebarObjective, objectiveCreate, title)})
		for i, line := range lines {
			out = append(out, create(i, line), score(i))
		}
		out = append(out, Packet{packetid.CbPlayDisplayObjective, displayObjectivePayload(sidebarSlot, sidebarObjective)})
		return next, out
	}
	if title != prev.title {
		out = append(out, Packet{packetid.CbPlayUpdateObjectives, objectivePayload(sidebarObjective, objectiveUpdate, title)})
	}
	rescore := len(lines) != len(prev.lines) // every position shifts
	for i, line := range lines {
		switch {
		case i >= len(prev.lines):
			out = append(out, create(i, line), score(i))
			continue
		case line != prev.lines[i]:
			out = append(out, teams(teamPayload(sidebarTeam(i), teamModeUpdate, colorReset, line, nil)))
		}
		if rescore {
			out = append(out, score(i))
		}
	}
	for i := len(lines); i < len(prev.lines); i++ {
		out = append(out,
			Packet{packetid.CbPlayUpdateScore, scoreRemovePayload(sidebarHolder(i), sidebarObjective)},
			teams(teamRemovePayload(sidebarTeam(i))))
	}
	return next, out
}

// ClearSidebar is the packets that take prev down (none for nil).
func ClearSidebar(prev *Sidebar) []Packet {
	if prev == nil {
		return nil
	}
	var out []Packet
	for i := range prev.lines {
		out = append(out, teams(teamRemovePayload(sidebarTeam(i))))
	}
	// Removing the objective drops its scores and the display slot.
	return append(out, Packet{packetid.CbPlayUpdateObjectives, objectivePayload(sidebarObjective, objectiveRemove, "")})
}
