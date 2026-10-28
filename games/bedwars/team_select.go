package bedwars

import (
	"fmt"
	"strings"

	"minecraft-server/game"
)

// team_select.go: picking a team while the round waits for players. Every
// waiting player holds a white wool "Select team"; using it opens a chest
// menu with one wool per team in the team's colour. The pick is a
// reservation, honoured by startRound while the team has room (teamSize);
// players who didn't pick — or whose team filled up — are dealt onto the
// smallest teams as before.

// nameTeamSelector is the display name of the selector wool.
const nameTeamSelector = "§fSelect team"

// teamMenuTitle is the selector menu's title.
const teamMenuTitle = "Select team"

// giveTeamSelector puts the selector wool in the first hotbar slot; it
// can't be dropped (or stashed in a chest).
func giveTeamSelector(p game.PlayerHandle) {
	p.SetSlot(game.SlotHotbar0, game.ItemStack{
		Item: "minecraft:white_wool", Count: 1, Name: nameTeamSelector,
		Lore:   []string{"§7Right-click to pick your team"},
		NoDrop: true,
	})
}

// teamMenuSlot is the menu slot of team i of n: spread across the single
// row with a gap between them when they fit (4 teams → slots 1, 3, 5, 7).
func teamMenuSlot(i, n int) int {
	if n <= 4 {
		return 2*i + (9-(2*n-1))/2
	}
	return i
}

// chosenCountLocked is how many waiting players picked team id.
func (g *bedWars) chosenCountLocked(id int) int {
	n := 0
	for _, t := range g.chosen {
		if t == id {
			n++
		}
	}
	return n
}

// openTeamMenu shows p the teams with how many picked each; p's own pick
// shimmers.
func (g *bedWars) openTeamMenu(p game.PlayerHandle) {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return
	}
	mine, picked := g.chosen[p.EntityID()]
	items := make([]game.MenuItem, 0, len(g.teams))
	slotTeam := make(map[int]int, len(g.teams))
	for i, ts := range g.teams {
		t := ts.team
		lore := []string{fmt.Sprintf("§7Players: §f%d§7/§f%d", g.chosenCountLocked(t.ID), g.teamSize)}
		selected := picked && mine == t.ID
		if selected {
			lore = append(lore, "§aSelected")
		} else {
			lore = append(lore, "§eClick to join")
		}
		slot := teamMenuSlot(i, len(g.teams))
		slotTeam[slot] = t.ID
		items = append(items, game.MenuItem{
			Slot: slot, Item: t.Wool.Name, Count: 1,
			Name: teamColour(t) + t.Name, Lore: lore, Glint: selected,
		})
	}
	g.mu.Unlock()

	p.OpenMenu(teamMenuTitle, 1, items, func(click game.MenuClick) {
		if id, ok := slotTeam[click.Slot]; ok {
			g.chooseTeam(p, id)
			g.openTeamMenu(p) // refresh the counts and the shimmer
		}
	})
}

// chooseTeam records p's pick of team id, unless that team is already full.
func (g *bedWars) chooseTeam(p game.PlayerHandle, id int) {
	g.mu.Lock()
	if g.started || id < 0 || id >= len(g.teams) {
		g.mu.Unlock()
		return
	}
	t := g.teams[id].team
	eid := p.EntityID()
	if cur, ok := g.chosen[eid]; ok && cur == id {
		g.mu.Unlock()
		return
	}
	if g.teamSize > 0 && g.chosenCountLocked(id) >= g.teamSize {
		g.mu.Unlock()
		p.SendMessage(fmt.Sprintf("§cThe %s team is full.", t.Name))
		return
	}
	g.chosen[eid] = id
	g.mu.Unlock()
	p.SendMessage(fmt.Sprintf("§7You picked the %s%s §7team.", teamColour(t), t.Name))
}

// teamColour is the § colour code matching a team's wool.
func teamColour(t Team) string {
	switch strings.TrimSuffix(strings.TrimPrefix(t.Wool.Name, "minecraft:"), "_wool") {
	case "red":
		return "§c"
	case "blue":
		return "§9"
	case "green":
		return "§a"
	case "yellow":
		return "§e"
	case "cyan":
		return "§3"
	case "pink":
		return "§d"
	case "gray":
		return "§8"
	}
	return "§f"
}

// pickedTeamLocked returns the team p reserved, if it still has room in the
// round being dealt (members are added as players are placed).
func (g *bedWars) pickedTeamLocked(eid int32) (*teamState, bool) {
	id, ok := g.chosen[eid]
	if !ok || id < 0 || id >= len(g.teams) {
		return nil, false
	}
	ts := g.teams[id]
	if g.teamSize > 0 && len(ts.members) >= g.teamSize {
		return nil, false
	}
	return ts, true
}
