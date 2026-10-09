package bedwars

import (
	"fmt"
	"math"
	"strings"
	"time"

	"minecraft-server/game"
	"minecraft-server/player"
)

// traps.go runs the base-area team upgrades: Heal Pool (Regeneration for
// members near their own spawn) and the trap queue (the first queued trap
// fires when an enemy enters the base radius). Both poll from OnTick.

const (
	// baseRadius is how far from the team spawn the base extends for Heal
	// Pool and traps.
	baseRadius = 20.0
	// healPoolInterval / trapScanInterval throttle the polls (ticks).
	healPoolInterval = 20
	trapScanInterval = 10

	healPoolDuration   = 3 * time.Second
	trapBlindDuration  = 8 * time.Second
	trapBoostDuration  = 15 * time.Second
	trapFatigueSeconds = 10 * time.Second
)

// nearBase reports whether a pose is within baseRadius of a team's spawn.
func (g *bedWars) nearBase(teamID int, pose player.Snapshot) bool {
	sp := g.arena.Spawns[teamID].Position
	dx := pose.X - (float64(sp.X) + 0.5)
	dy := pose.Y - float64(sp.Y)
	dz := pose.Z - (float64(sp.Z) + 0.5)
	return math.Sqrt(dx*dx+dy*dy+dz*dz) <= baseRadius
}

// healPoolTick regenerates members standing in their own base.
func (g *bedWars) healPoolTick(ctx *game.Ctx, tick uint64) {
	if tick%healPoolInterval != 0 {
		return
	}
	players := ctx.Instance.Players()
	g.mu.Lock()
	var heal []game.PlayerHandle
	for _, p := range players {
		teamID, ok := g.byEntity[p.EntityID()]
		if !ok || !g.teams[teamID].healPool {
			continue
		}
		pose := p.Pose()
		if pose.Dead || pose.Gamemode == player.Spectator || !g.nearBase(teamID, pose) {
			continue
		}
		heal = append(heal, p)
	}
	g.mu.Unlock()
	for _, p := range heal {
		p.ApplyEffect("regeneration", 1, healPoolDuration)
	}
}

// trapTick fires the first queued trap of a team when an enemy enters its
// base (edge-triggered: a player already inside doesn't retrigger).
func (g *bedWars) trapTick(ctx *game.Ctx, tick uint64) {
	if tick%trapScanInterval != 0 {
		return
	}
	players := ctx.Instance.Players()

	type firing struct {
		ts       *teamState
		kind     trapKind
		intruder game.PlayerHandle
	}
	var fire []firing

	g.mu.Lock()
	for _, ts := range g.teams {
		for _, p := range players {
			eid := p.EntityID()
			teamID, ok := g.byEntity[eid]
			if !ok || teamID == ts.team.ID {
				continue
			}
			pose := p.Pose()
			inside := !pose.Dead && pose.Gamemode != player.Spectator && g.nearBase(ts.team.ID, pose)
			was := ts.inside[eid]
			ts.inside[eid] = inside
			if inside && !was && len(ts.traps) > 0 {
				kind := ts.traps[0]
				ts.traps = ts.traps[1:]
				fire = append(fire, firing{ts: ts, kind: kind, intruder: p})
			}
		}
	}
	g.mu.Unlock()

	for _, f := range fire {
		g.fireTrap(ctx, f.ts, f.kind, f.intruder)
	}
}

// fireTrap applies one trap's effects and alerts the defending team.
func (g *bedWars) fireTrap(ctx *game.Ctx, ts *teamState, kind trapKind, intruder game.PlayerHandle) {
	g.mu.Lock()
	defenders := g.teamMembersLocked(ctx, ts)
	g.mu.Unlock()

	switch kind {
	case trapBlind:
		intruder.ApplyEffect("blindness", 1, trapBlindDuration)
		intruder.ApplyEffect("slowness", 1, trapBlindDuration)
	case trapCounter:
		for _, d := range defenders {
			if g.nearBase(ts.team.ID, d.Pose()) {
				d.ApplyEffect("speed", 2, trapBoostDuration)
				d.ApplyEffect("jump_boost", 2, trapBoostDuration)
			}
		}
	case trapAlarm:
		intruder.RemoveEffect("invisibility")
	case trapFatigue:
		intruder.ApplyEffect("mining_fatigue", 1, trapFatigueSeconds)
	}

	label := strings.TrimPrefix(kind.def().Label, "§a")
	sub := fmt.Sprintf("§7%s triggered %s", intruder.Name(), label)
	if kind == trapAlarm {
		if team, ok := g.teamOf(intruder); ok {
			sub = fmt.Sprintf("§7%s §7(%s team) is in your base!", intruder.Name(), team.Name)
		}
	}
	for _, d := range defenders {
		d.SendTitle("§c§lTRAP TRIGGERED!", sub, 0, 40, 10)
		d.PlaySound("minecraft:entity.ender_dragon.growl", 1, 1)
		d.SendMessage(fmt.Sprintf("§c§lTRAP TRIGGERED! §r%s", sub))
	}
}
