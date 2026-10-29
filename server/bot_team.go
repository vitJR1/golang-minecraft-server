package server

import (
	"fmt"
	"sort"

	"minecraft-server/anticheat"
	"minecraft-server/world"
)

// bot_team.go: a team's bots (game.BotPlan.Team) share one brain, the
// teamBrain, that decides every botTeamEvery ticks who does what:
//
//   - roles, by entity id among the team's living bots: the first is the
//     pusher (bot_pusher.go) while any enemy bed stands, a warrior after; then defenders — one when there are two or more bots
//     and the bed stands, two while an enemy is at the bed and there are
//     three or more — who wrap the bed in two layers of blocks and then go
//     and fight with the team; then, in a team of three or more, an archer if a bot
//     is still free; the rest attack;
//   - one bed for every attacker to go for (the lead's nearest), so they
//     push together instead of spreading over the map;
//   - an intruder: the enemy nearest the team's bed within botBaseRadius,
//     whom defenders go for from farther than their aggro range.
//
// A lone bot attacks; with the bed gone everyone does.

type botRole int

const (
	roleAttacker botRole = iota // the warrior: fights, raids, goes for beds
	roleDefender
	roleArcher
	rolePusher // the beds at any cost (bot_pusher.go)
)

func (r botRole) String() string {
	switch r {
	case roleDefender:
		return "defender"
	case roleArcher:
		return "archer"
	case rolePusher:
		return "pusher"
	}
	return "attacker"
}

const (
	botTeamEvery   = 10   // ticks between team decisions
	botBaseRadius  = 16.0 // an enemy this close to the bed is an intruder
	botArcherRange = 7.0  // an archer shoots enemies farther than this
	botArcherSight = 70.0 // an archer with a bow engages enemies it sees this far off
	botArcherRise  = 24.0 // … this far above or below it
)

// teamBrain is the shared decision-making of one team's bots.
type teamBrain struct {
	at       uint64
	roles    map[int32]botRole
	focus    world.Position // the bed the attackers go for
	hasFocus bool
	intruder *ClientConnection
	// falls counts the team's bots' falls by the route cell they fell
	// from (bot_reinforce.go).
	falls map[world.Position]int
}

// teamBrainFor returns (creating) the brain of the bot's team; nil for a bot
// without a team.
func (b *bot) teamBrainFor() *teamBrain {
	team := b.brain.plan.Team
	inst := b.c.instance
	if team == "" || inst == nil {
		return nil
	}
	if inst.botTeams == nil {
		inst.botTeams = map[string]*teamBrain{}
	}
	tb := inst.botTeams[team]
	if tb == nil {
		tb = &teamBrain{roles: map[int32]botRole{}}
		inst.botTeams[team] = tb
	}
	return tb
}

// teamUpdate refreshes the team's decisions when they're due and applies
// this bot's part: its role.
func (b *bot) teamUpdate(now uint64) {
	tb := b.teamBrainFor()
	if tb == nil {
		b.setRole(roleAttacker)
		return
	}
	if tb.at == 0 || now >= tb.at+botTeamEvery {
		tb.decide(now, b.c.instance, b.brain.plan.Team)
	}
	b.setRole(tb.roles[b.c.player.EntityID])
}

// setRole takes up a role and, when it changed, shows it in the name tag
// (tab list, over the head) to everyone in the instance.
func (b *bot) setRole(r botRole) {
	b.brain.role = r
	if old := b.roleShown.Swap(int32(r)); old != int32(r) {
		inst := b.c.instance
		inst.Players.Range(func(c *ClientConnection) { c.syncNameTags(inst) })
	}
}

// shownRole is the role the bot's name tag shows (read from any goroutine);
// false before it has one.
func (b *bot) shownRole() (botRole, bool) {
	r := b.roleShown.Load()
	return botRole(r), r >= 0
}

// label is the role's name-tag text; code its short form for team names
// (≤16 characters with the game's group).
func (r botRole) label() string {
	switch r {
	case rolePusher:
		return "§6[Pusher]"
	case roleArcher:
		return "§b[Archer]"
	case roleDefender:
		return "§a[Defender]"
	}
	return "§c[Warrior]"
}

func (r botRole) code() string { return string("wdap"[r]) }

// decide works out the team's roles, focus and intruder. Bots tick one after
// another on the instance's tick goroutine, so reading their plans is safe.
func (tb *teamBrain) decide(now uint64, inst *Instance, team string) {
	tb.at = now
	var mates []*ClientConnection
	inst.Players.Range(func(o *ClientConnection) {
		if o.isBot() && o.bot.ready.Load() && o.bot.brain.plan.Team == team && !o.bot.brain.plan.Idle &&
			!o.player.Snapshot().Dead {
			mates = append(mates, o)
		}
	})
	sort.Slice(mates, func(i, j int) bool { return mates[i].player.EntityID < mates[j].player.EntityID })
	tb.roles = map[int32]botRole{}
	tb.intruder, tb.hasFocus = nil, false
	if len(mates) == 0 {
		return
	}
	lead := mates[0].bot

	// An enemy at the bed?
	bed := lead.brain.plan.Defend
	if len(bed) > 0 {
		centre := blockCentre(bed[0])
		best := botBaseRadius
		for _, o := range inst.Players.snapshot() {
			if o.isBot() && o.bot.brain.plan.Team == team || !lead.targetValid(o) {
				continue
			}
			s := o.player.Snapshot()
			if d := anticheat.Dist(centre, anticheat.Vec{X: s.X, Y: s.Y, Z: s.Z}); d <= best {
				tb.intruder, best = o, d
			}
		}
	}

	defenders, archers := 0, 0
	if len(bed) > 0 && len(mates) >= 2 {
		defenders = 1
		if tb.intruder != nil && len(mates) >= 3 {
			defenders = 2
		}
	}
	if len(mates) >= 3 && len(mates)-1-defenders >= 1 {
		archers = 1
	}
	for i, m := range mates {
		role := roleAttacker
		switch {
		case i == 0:
			if len(lead.brain.plan.Targets) > 0 {
				role = rolePusher // a warrior once no enemy bed is left
			}
		case i <= defenders:
			role = roleDefender
		case i <= defenders+archers:
			role = roleArcher
		}
		tb.roles[m.player.EntityID] = role
	}

	// The bed to push on together: the lead's nearest it hasn't given up on.
	for _, t := range lead.brain.plan.Targets {
		if until, ok := lead.brain.failed[t]; ok && now < until {
			continue
		}
		tb.focus, tb.hasFocus = t, true
		break
	}
}

// teamTargets orders the plan's targets with the team's focus first.
func (b *bot) teamTargets() []world.Position {
	targets := b.brain.plan.Targets
	tb := b.teamBrainFor()
	if tb == nil || !tb.hasFocus || len(targets) == 0 || targets[0] == tb.focus {
		return targets
	}
	out := []world.Position{tb.focus}
	found := false
	for _, t := range targets {
		if t == tb.focus {
			found = true
			continue
		}
		out = append(out, t)
	}
	if !found {
		return targets
	}
	return out
}

// teamIntruder is the enemy at the team's bed that this bot, a defender,
// should go for — or nil.
func (b *bot) teamIntruder() *ClientConnection {
	if b.brain.role != roleDefender {
		return nil
	}
	tb := b.teamBrainFor()
	if tb == nil || tb.intruder == nil || !b.targetValid(tb.intruder) {
		return nil
	}
	return tb.intruder
}

// shellAround lists the cells wrapping the given ones — at the sides and on
// top, not underneath — in order, sides first.
func shellAround(inner []world.Position) []world.Position {
	in := map[world.Position]bool{}
	for _, p := range inner {
		in[p] = true
	}
	var out []world.Position
	seen := map[world.Position]bool{}
	for _, d := range [5][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {0, 1, 0}} {
		for _, p := range inner {
			c := p.Add(d[0], d[1], d[2])
			if in[c] || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// openCells keeps the cells a block can still go into.
func openCells(at blockAt, cells []world.Position) []world.Position {
	var out []world.Position
	for _, c := range cells {
		if world.Replaceable(at(c)) {
			out = append(out, c)
		}
	}
	return out
}

// bedCover lists the cells round the bed blocks still to fill: on top and
// at the sides (not the bed's own other half).
func bedCover(at blockAt, bed []world.Position) []world.Position {
	return openCells(at, shellAround(bed))
}

// bedOuterCover is the second layer: the shell round the bed and its first
// layer, still to fill.
func bedOuterCover(at blockAt, bed []world.Position) []world.Position {
	return openCells(at, shellAround(append(append([]world.Position{}, bed...), shellAround(bed)...)))
}

// defend covers the team's bed with two layers of blocks. The bool is
// false when there's nothing (more) to do here — no bed, or both layers
// built: the defender then joins the team's attack (the team brain calls it
// back for an intruder). Out of blocks with the cover unfinished, it goes to
// the station to earn more.
func (b *bot) defend(now uint64, blocks int) (physInput, bool) {
	c, br := b.c, &b.brain
	plan := br.plan
	if len(plan.Defend) == 0 {
		return physInput{}, false
	}
	at := c.instance.World.GetBlock
	// Cells it couldn't get to or fill lately are left out.
	usable := func(cells []world.Position) []world.Position {
		var out []world.Position
		for _, p := range cells {
			if now >= br.coverFail[p] {
				out = append(out, p)
			}
		}
		return out
	}
	cover := usable(bedCover(at, plan.Defend))
	if len(cover) == 0 {
		cover = usable(bedOuterCover(at, plan.Defend))
	}
	if len(cover) == 0 {
		return physInput{}, false // fortified (as far as it can)
	}
	if blocks == 0 {
		if plan.HasStation {
			return b.goTo(now, "station", plan.Station, atStation(plan.Station), blocks), true
		}
		return physInput{}, false
	}
	// Place what's in reach; walk up to the rest.
	for _, p := range cover {
		if anticheat.Dist(c.eye(), blockCentre(p)) <= botDigReach && !c.instance.blockedByPlayer(p) {
			b.lookAt(blockCentre(p))
			if now >= br.nextPlace {
				br.nextPlace = now + b.skill.placeGap
				if !c.botPlace(p, plan.BlockItem) {
					b.skipCover(now, p)
				}
			}
			return physInput{}, true
		}
	}
	aim := cover[0]
	coverSet := map[world.Position]bool{}
	for _, p := range cover {
		coverSet[p] = true
	}
	goal := func(cell world.Position) bool {
		if coverSet[cell] || coverSet[cell.Add(0, 1, 0)] {
			return false // don't stand where a block goes
		}
		eye := anticheat.Vec{X: float64(cell.X) + 0.5, Y: float64(cell.Y) + anticheat.EyeHeight, Z: float64(cell.Z) + 0.5}
		return anticheat.Dist(eye, blockCentre(aim)) <= botDigReach-0.5
	}
	in, status := b.goToFar(now, fmt.Sprintf("defend:%v", aim), aim, goal, 0)
	if status == farFailed {
		b.skipCover(now, aim) // nowhere to stand for it
	}
	return in, true
}

// botCoverRetry is how long a cover cell that couldn't be filled is skipped.
const botCoverRetry = 600

// skipCover leaves cover cell p out for a while.
func (b *bot) skipCover(now uint64, p world.Position) {
	if b.brain.coverFail == nil {
		b.brain.coverFail = map[world.Position]uint64{}
	}
	b.brain.coverFail[p] = now + botCoverRetry
}
