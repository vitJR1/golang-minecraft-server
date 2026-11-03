package server

import (
	"minecraft-server/game"
	"minecraft-server/server/hud/scoreboard"
)

// name_tags.go: players grouped under a coloured prefix — "● Name" in the
// tab list and over the head — through scoreboard teams, which the client
// applies to both. A game puts a player in a group (game.NameTag, e.g. their
// BedWars team); everyone in the instance gets the group as a team "nt_…"
// with the prefix, the name colour and the members.
//
// The groups and their packets are server/hud/scoreboard (Tags, SyncTags).
// Like the sidebar (sidebar.go), the client keeps its teams across our
// Respawn and kicks on a team created twice, so each connection remembers
// what it has (nameTags, under sidebarMu) and syncNameTags sends only the
// difference: on every change in the instance and when it joins one (which
// also drops the old instance's groups).

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
func (i *Instance) nameTagGroups() scoreboard.Tags {
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
		role, ok := c.bot.ShownRole()
		if !ok {
			return
		}
		tag := tags[c.playerName]
		if tag.Group == "" {
			tag.Group = "bot"
		}
		tag.Group += "_" + role.Code()
		tag.Prefix += role.Label() + " "
		tags[c.playerName] = tag
	})
	out := scoreboard.Tags{}
	for name, tag := range tags {
		out.Add(tag.Group, tag.Prefix, tag.Colour, name)
	}
	return out
}

// syncNameTags brings the client's groups in line with inst's.
func (c *ClientConnection) syncNameTags(inst *Instance) {
	if c.player == nil {
		return
	}
	want := scoreboard.Tags{}
	if inst != nil {
		want = inst.nameTagGroups()
	}
	c.sidebarMu.Lock()
	defer c.sidebarMu.Unlock()
	c.writeScoreboard(scoreboard.SyncTags(c.nameTags, want))
	c.nameTags = want
}
