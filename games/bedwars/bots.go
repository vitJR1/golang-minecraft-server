package bedwars

import (
	"fmt"
	"sort"
	"strings"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// bots.go is BedWars' side of the server's bots (game.BotGuide): which team
// a bot joins, who its enemies are, and its plan — collect iron at the team
// forge, buy wool at the team's item shop, bridge to the nearest enemy bed
// and break it. The bot core (movement, bridging, fighting, digging) is the
// server's.

// botBlocksKey is what bots buy to build with.
const botBlocksKey = "wool"

// teamByName finds a team of this round by name, case-insensitively.
func (g *bedWars) teamByName(name string) (*teamState, bool) {
	for _, ts := range g.teams {
		if strings.EqualFold(ts.team.Name, name) {
			return ts, true
		}
	}
	return nil, false
}

// BotJoin puts the bot on the named team: as its pick while the round waits,
// or straight onto the team (moved there, re-kitted) when it's running.
func (g *bedWars) BotJoin(_ *game.Ctx, p game.PlayerHandle, team string) error {
	g.mu.Lock()
	ts, ok := g.teamByName(team)
	if !ok {
		names := make([]string, len(g.teams))
		for i, t := range g.teams {
			names[i] = strings.ToLower(t.team.Name)
		}
		g.mu.Unlock()
		return fmt.Errorf("no team %q (teams: %s)", team, strings.Join(names, ", "))
	}
	eid := p.EntityID()
	if !g.started {
		if g.teamSize > 0 && g.chosenCountLocked(ts.team.ID) >= g.teamSize {
			g.mu.Unlock()
			return fmt.Errorf("the %s team is full", ts.team.Name)
		}
		g.chosen[eid] = ts.team.ID
		g.mu.Unlock()
		return nil
	}
	if cur, ok := g.byEntity[eid]; ok && cur == ts.team.ID {
		g.mu.Unlock()
		return nil
	}
	if g.teamSize > 0 && len(ts.members) >= g.teamSize {
		g.mu.Unlock()
		return fmt.Errorf("the %s team is full", ts.team.Name)
	}
	if cur, ok := g.byEntity[eid]; ok {
		delete(g.teams[cur].members, eid)
	}
	ts.members[eid] = true
	g.byEntity[eid] = ts.team.ID
	k := g.kitOfLocked(eid)
	spawn := g.arena.Spawns[ts.team.ID]
	if g.populatedTeams() >= 2 {
		g.engaged = true
	}
	g.mu.Unlock()

	teleport(p, spawn)
	g.equip(p, ts, k)
	return nil
}

// BotEnemy: during the round, a living player of another team.
func (g *bedWars) BotEnemy(_ *game.Ctx, p, other game.PlayerHandle) bool {
	pose := other.Pose()
	if pose.Dead || pose.Gamemode == player.Spectator {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	mine, ok1 := g.byEntity[p.EntityID()]
	theirs, ok2 := g.byEntity[other.EntityID()]
	return g.started && ok1 && ok2 && mine != theirs
}

// BotPlan: idle while waiting, dead or spectating; otherwise the team's
// iron forge to collect at, its item shop to buy wool at, and the enemy beds
// still standing, nearest first.
func (g *bedWars) BotPlan(_ *game.Ctx, p game.PlayerHandle) game.BotPlan {
	pose := p.Pose()
	if pose.Dead || pose.Gamemode == player.Spectator {
		return game.BotPlan{Idle: true}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.byEntity[p.EntityID()]
	if !g.started || !ok {
		return game.BotPlan{Idle: true}
	}
	plan := game.BotPlan{Home: g.arena.Spawns[id].Position}
	for _, gen := range g.gens {
		if gen.TeamID == id && gen.Resource == Iron {
			plan.Station, plan.HasStation = gen.Pos, true
			break
		}
	}
	if !plan.HasStation {
		plan.Station, plan.HasStation = plan.Home, true
	}
	for pos, v := range g.arena.Villagers {
		if v.Team == id && v.Kind != "upgrade" {
			plan.Shop, plan.HasShop = pos, true
			break
		}
	}
	if it, ok := findShopItem(botBlocksKey); ok {
		plan.BuyKey = it.Key
		plan.BlockItem = resolveGoods(it.Icon, g.teams[id].team)
		plan.Currency = it.Cost.item()
		plan.Price = it.Price
		plan.Pack = it.Count
	}

	type bed struct {
		pos  world.Position
		dist float64
	}
	var beds []bed
	for _, ts := range g.teams {
		if ts.team.ID == id || !ts.bedAlive || ts.team.ID >= len(g.arena.BedBlocks) {
			continue
		}
		for _, b := range g.arena.BedBlocks[ts.team.ID] {
			dx, dy, dz := float64(b.X)-pose.X, float64(b.Y)-pose.Y, float64(b.Z)-pose.Z
			beds = append(beds, bed{b, dx*dx + dy*dy + dz*dz})
			break // one half is enough: breaking it takes both
		}
	}
	sort.Slice(beds, func(i, j int) bool { return beds[i].dist < beds[j].dist })
	for _, b := range beds {
		plan.Targets = append(plan.Targets, b.pos)
	}
	return plan
}

// BotBuy buys the goods with this key as a click in the shop would; true
// when the bot got them.
func (g *bedWars) BotBuy(_ *game.Ctx, p game.PlayerHandle, key string) bool {
	it, ok := findShopItem(key)
	if !ok {
		return false
	}
	ts, _, ok := g.playerState(p)
	if !ok {
		return false
	}
	goods := resolveGoods(it.Icon, ts.team)
	before := p.CountItem(goods)
	g.buyItem(p, it, false)
	return p.CountItem(goods) > before
}
