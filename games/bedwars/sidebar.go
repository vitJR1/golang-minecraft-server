package bedwars

import (
	"fmt"

	"minecraft-server/game"
)

// sidebar.go: the shared-generator tiers and the scoreboard sidebar.
//
// Generator tiers follow the Hypixel event timeline: the diamond and
// emerald generators start at tier I and speed up at fixed times into the
// round (Diamond II at 6:00, Emerald II at 12:00, Diamond III at 18:00,
// Emerald III at 24:00). A tier scales the generator's configured interval
// (genTierSpeed); team forges are unaffected (they have Forge upgrades).
//
// The sidebar (PlayerHandle.SetSidebar) is per player and refreshed once a
// second from OnTick: the player's kills and beds broken, the current
// diamond / emerald tier, the countdown to the next upgrade and the
// player's team.

const sidebarTitle = "§e§lBED WARS"

// playerStats is what the sidebar counts for one player.
type playerStats struct {
	kills      int
	finalKills int
	beds       int
}

// statsLocked returns (creating) a player's stats. Caller holds g.mu.
func (g *bedWars) statsLocked(eid int32) *playerStats {
	st := g.stats[eid]
	if st == nil {
		st = &playerStats{}
		g.stats[eid] = st
	}
	return st
}

// genUpgrade is one step of the shared-generator timeline.
type genUpgrade struct {
	at   uint64 // instance tick
	res  Resource
	tier int
}

// genUpgrades is the Hypixel order; ticks at 20/s.
var genUpgrades = []genUpgrade{
	{6 * 60 * 20, Diamond, 2},
	{12 * 60 * 20, Emerald, 2},
	{18 * 60 * 20, Diamond, 3},
	{24 * 60 * 20, Emerald, 3},
}

// genTierSpeed is the interval multiplier per tier (index = tier): II runs
// at ¾ of the configured interval, III at ⅖ (Hypixel: diamond 30/23/12 s,
// emerald 65/45/25 s).
var genTierSpeed = [...]float64{1, 1, 0.75, 0.4}

// genMaxTier is the last tier a shared generator reaches.
const genMaxTier = 3

// genTierAt is the tier of res's generators at tick.
func genTierAt(res Resource, tick uint64) int {
	tier := 1
	for _, u := range genUpgrades {
		if u.res == res && tick >= u.at && u.tier > tier {
			tier = u.tier
		}
	}
	return tier
}

// nextGenUpgrade is the first upgrade still ahead of tick.
func nextGenUpgrade(tick uint64) (genUpgrade, bool) {
	for _, u := range genUpgrades {
		if u.at > tick {
			return u, true
		}
	}
	return genUpgrade{}, false
}

// roundTickLocked converts an instance tick to ticks since the round
// started (the generator timeline's clock). Caller holds g.mu.
func (g *bedWars) roundTickLocked(tick uint64) uint64 {
	if !g.started || tick < g.startTick {
		return 0
	}
	return tick - g.startTick
}

// announceUpgrades tells everyone when a generator tier kicks in.
func (g *bedWars) announceUpgrades(ctx *game.Ctx, tick uint64) {
	g.mu.Lock()
	rt := g.roundTickLocked(tick)
	g.mu.Unlock()
	for _, u := range genUpgrades {
		if u.at != rt || rt == 0 {
			continue
		}
		ctx.Instance.BroadcastChat("", fmt.Sprintf("%s◆ %s Generators §ehave been upgraded to %sTier %s",
			u.res.colour(), u.res, u.res.colour(), roman(u.tier)))
		for _, p := range ctx.Instance.Players() {
			p.PlaySound("minecraft:block.note_block.pling", 1, 1.5)
		}
	}
}

// refreshSidebars pushes every player's current sidebar; the server sends
// only the lines that changed.
func (g *bedWars) refreshSidebars(ctx *game.Ctx, tick uint64) {
	for _, p := range ctx.Instance.Players() {
		p.SetSidebar(sidebarTitle, g.sidebarLines(p, tick))
	}
}

// sidebarLines renders p's sidebar for tick.
func (g *bedWars) sidebarLines(p game.PlayerHandle, tick uint64) []string {
	g.mu.Lock()
	if !g.started {
		g.mu.Unlock()
		return g.lobbySidebarLines(tick)
	}
	tick = g.roundTickLocked(tick)
	var st playerStats
	if s := g.stats[p.EntityID()]; s != nil {
		st = *s
	}
	teamName := ""
	if id, ok := g.byEntity[p.EntityID()]; ok {
		teamName = g.teams[id].team.Name
	}
	g.mu.Unlock()

	lines := []string{
		fmt.Sprintf("§fKills: §a%d", st.kills),
		fmt.Sprintf("§fBeds broken: §a%d", st.beds),
		"",
		fmt.Sprintf("§b◆ Diamond: §fTier %s", roman(genTierAt(Diamond, tick))),
		fmt.Sprintf("§a◆ Emerald: §fTier %s", roman(genTierAt(Emerald, tick))),
	}
	if u, ok := nextGenUpgrade(tick); ok {
		lines = append(lines, fmt.Sprintf("§eNext: %s%s %s §ein §f%s", u.res.colour(), u.res, roman(u.tier), clock(u.at-tick)))
	} else {
		lines = append(lines, "§eGenerators maxed")
	}
	if teamName != "" {
		lines = append(lines, "", "§fTeam: §e"+teamName)
	}
	return lines
}

// clock renders ticks as m:ss.
func clock(ticks uint64) string {
	secs := (ticks + 19) / 20
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}
