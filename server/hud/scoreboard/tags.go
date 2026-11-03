package scoreboard

import (
	"sort"
	"strings"
)

// Name tags: players grouped under a coloured prefix — "● Name" in the
// tab list and over the head — through scoreboard teams, which the client
// applies to both. A game puts a player in a group (game.NameTag, e.g. their
// BedWars team); everyone in the instance gets the group as a team "nt_…"
// with the prefix, the name colour and the members.
//
// Like the sidebar, each connection remembers the groups it has and
// SyncTags sends only the difference.

const tagTeamPrefix = "nt_" // team names are ≤16 chars

// Tag is one group as a client has (or should have) it.
type Tag struct {
	prefix  string
	colour  int32
	members map[string]bool
}

// Tags are groups by team name.
type Tags map[string]Tag

// Add puts member into group (team "nt_<group>", cut to 16 characters) with
// the prefix and the § colour code of the name; the first member's prefix
// and colour make the group's.
func (t Tags) Add(group, prefix string, colour byte, member string) {
	team := tagTeamPrefix + group
	if len(team) > 16 {
		team = team[:16]
	}
	g, ok := t[team]
	if !ok {
		g = Tag{prefix: prefix, colour: ChatFormatting(colour), members: map[string]bool{}}
	}
	g.members[member] = true
	t[team] = g
}

// ChatFormatting maps a § colour code to the Update Teams "formatting"
// value (ChatFormatting's ordinal: 0..15 are the colours in code order).
func ChatFormatting(code byte) int32 {
	if i := strings.IndexByte("0123456789abcdef", code); i >= 0 {
		return int32(i)
	}
	return colorReset
}

// SyncTags is the packets that turn the groups a client has into want:
// new groups created with their members, changed prefixes / colours
// updated, members added and removed, gone groups removed.
//
// Order matters: the vanilla client moves a player that joins a team out of
// its old one, and "remove X from A" for a player no longer in A throws
// (Scoreboard.removePlayerFromTeam → IllegalStateException → kick). So every
// member removal and every gone group goes first, then the creates, updates
// and additions; teams are walked in sorted order so the output is
// deterministic.
func SyncTags(have, want Tags) []Packet {
	var out []Packet
	for _, team := range sortedTeams(have) {
		h := have[team]
		w, ok := want[team]
		if !ok {
			out = append(out, teams(teamRemovePayload(team)))
			continue
		}
		var remove []string
		for n := range h.members {
			if !w.members[n] {
				remove = append(remove, n)
			}
		}
		if len(remove) > 0 {
			sort.Strings(remove)
			out = append(out, teams(teamMembersPayload(team, teamModeRemoveMembers, remove)))
		}
	}
	for _, team := range sortedTeams(want) {
		w := want[team]
		h, ok := have[team]
		if !ok {
			out = append(out, teams(teamPayload(team, teamModeCreate, w.colour, w.prefix, sortedNames(w.members))))
			continue
		}
		if h.prefix != w.prefix || h.colour != w.colour {
			out = append(out, teams(teamPayload(team, teamModeUpdate, w.colour, w.prefix, nil)))
		}
		var add []string
		for n := range w.members {
			if !h.members[n] {
				add = append(add, n)
			}
		}
		if len(add) > 0 {
			sort.Strings(add)
			out = append(out, teams(teamMembersPayload(team, teamModeAddMembers, add)))
		}
	}
	return out
}

func sortedTeams(t Tags) []string {
	out := make([]string, 0, len(t))
	for team := range t {
		out = append(out, team)
	}
	sort.Strings(out)
	return out
}

func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
