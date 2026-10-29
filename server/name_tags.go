package server

import (
	"bytes"
	"sort"
	"strings"

	"minecraft-server/game"
	"minecraft-server/protocol"
)

// name_tags.go: players grouped under a coloured prefix — "● Name" in the
// tab list and over the head — through scoreboard teams, which the client
// applies to both. A game puts a player in a group (game.NameTag, e.g. their
// BedWars team); everyone in the instance gets the group as a team "nt_…"
// with the prefix, the name colour and the members.
//
// Like the sidebar (sidebar.go), the client keeps its teams across our
// Respawn and kicks on a team created twice, so each connection remembers
// what it has (nameTags, under sidebarMu) and syncNameTags sends only the
// difference: on every change in the instance and when it joins one (which
// also drops the old instance's groups).

const nameTagTeamPrefix = "nt_" // team names are ≤16 chars

// shownTag is one group as a client has (or should have) it.
type shownTag struct {
	prefix  string
	colour  int32
	members map[string]bool
}

// chatFormatting maps a § colour code to the Update Teams "formatting"
// value (ChatFormatting's ordinal: 0..15 are the colours in code order).
func chatFormatting(code byte) int32 {
	if i := strings.IndexByte("0123456789abcdef", code); i >= 0 {
		return int32(i)
	}
	return teamColorReset
}

// setNameTag puts the named player into tag's group (an empty Group takes
// them out) and updates everyone in the instance.
func (i *Instance) setNameTag(name string, tag game.NameTag) {
	i.tagsMu.Lock()
	if tag.Group == "" {
		delete(i.tags, name)
	} else {
		if i.tags == nil {
			i.tags = map[string]game.NameTag{}
		}
		i.tags[name] = tag
	}
	i.tagsMu.Unlock()
	i.Players.Range(func(c *ClientConnection) { c.syncNameTags(i) })
}

// nameTagGroups is the instance's groups by team name.
func (i *Instance) nameTagGroups() map[string]shownTag {
	i.tagsMu.Lock()
	tags := make(map[string]game.NameTag, len(i.tags))
	for name, tag := range i.tags {
		tags[name] = tag
	}
	i.tagsMu.Unlock()
	// A bot shows the part it plays: its role joins its group and prefix
	// ("● [Pusher] BOT_Max"), and a bot the game hasn't tagged gets a tag
	// of its own.
	i.Players.Range(func(c *ClientConnection) {
		if !c.isBot() {
			return
		}
		role, ok := c.bot.shownRole()
		if !ok {
			return
		}
		tag := tags[c.playerName]
		if tag.Group == "" {
			tag.Group = "bot"
		}
		tag.Group += "_" + role.code()
		tag.Prefix += role.label() + " "
		tags[c.playerName] = tag
	})
	out := map[string]shownTag{}
	for name, tag := range tags {
		team := nameTagTeamPrefix + tag.Group
		if len(team) > 16 {
			team = team[:16]
		}
		g, ok := out[team]
		if !ok {
			g = shownTag{prefix: tag.Prefix, colour: chatFormatting(tag.Colour), members: map[string]bool{}}
		}
		g.members[name] = true
		out[team] = g
	}
	return out
}

// syncNameTags brings the client's groups in line with inst's.
func (c *ClientConnection) syncNameTags(inst *Instance) {
	if c.player == nil {
		return
	}
	want := map[string]shownTag{}
	if inst != nil {
		want = inst.nameTagGroups()
	}
	c.sidebarMu.Lock()
	defer c.sidebarMu.Unlock()
	have := c.nameTags
	for team, w := range want {
		h, ok := have[team]
		if !ok {
			_ = c.safeWrite(CbPlayUpdateTeams, tagTeamPayload(team, teamModeCreate, w, sortedNames(w.members)))
			continue
		}
		if h.prefix != w.prefix || h.colour != w.colour {
			_ = c.safeWrite(CbPlayUpdateTeams, tagTeamPayload(team, teamModeUpdate, w, nil))
		}
		var add, remove []string
		for n := range w.members {
			if !h.members[n] {
				add = append(add, n)
			}
		}
		for n := range h.members {
			if !w.members[n] {
				remove = append(remove, n)
			}
		}
		if len(add) > 0 {
			sort.Strings(add)
			_ = c.safeWrite(CbPlayUpdateTeams, teamMembersPayload(team, teamModeAddMembers, add))
		}
		if len(remove) > 0 {
			sort.Strings(remove)
			_ = c.safeWrite(CbPlayUpdateTeams, teamMembersPayload(team, teamModeRemoveMembers, remove))
		}
	}
	for team := range have {
		if _, ok := want[team]; !ok {
			_ = c.safeWrite(CbPlayUpdateTeams, teamRemovePayload(team))
		}
	}
	c.nameTags = want
}

func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Update Teams modes 3 / 4 (minecraft-data 1.20: "players" array).
const (
	teamModeAddMembers    byte = 3
	teamModeRemoveMembers byte = 4
)

// tagTeamPayload builds Update Teams mode 0 (with members) or 2 for a group.
func tagTeamPayload(team string, mode byte, t shownTag, members []string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(team))
	buf.WriteByte(mode)
	buf.Write(textComponentPayload(team))
	buf.WriteByte(0) // friendly flags
	buf.Write(protocol.WriteString("always"))
	buf.Write(protocol.WriteString("always"))
	protocol.WriteVarInt32ToBuffer(&buf, t.colour)
	buf.Write(textComponentPayload(t.prefix))
	buf.Write(textComponentPayload(""))
	if mode == teamModeCreate {
		protocol.WriteVarInt32ToBuffer(&buf, int32(len(members)))
		for _, m := range members {
			buf.Write(protocol.WriteString(m))
		}
	}
	return buf.Bytes()
}

// teamMembersPayload builds Update Teams mode 3 / 4.
func teamMembersPayload(team string, mode byte, members []string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(team))
	buf.WriteByte(mode)
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(members)))
	for _, m := range members {
		buf.Write(protocol.WriteString(m))
	}
	return buf.Bytes()
}
