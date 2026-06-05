// Package bedwars is a 4-team BedWars mini-game.
//
// Rules implemented on today's engine:
//   - Four teams (Red/Blue/Green/Yellow), each on its own island with a
//     bed. Joining players are balanced across teams.
//   - Break an enemy team's bed (OnBlockBreak) and that team can no longer
//     respawn. You cannot break your own bed, the map, or anything you
//     didn't place — only player-placed blocks and beds are breakable.
//   - A "kill" (the OnPlayerAttack hook — the server has no damage system,
//     so one hit is a kill) or a fall into the void respawns the victim at
//     their island if their bed is alive, or eliminates them (→ Spectator)
//     if it isn't.
//   - Last team with a living member wins; the instance then returns
//     everyone to the hub.
//
// Deliberately seam-only for now (the engine doesn't expose item-give to
// plugins): resource generators tick on a schedule but hand off to a
// ResourceGranter (default no-op), and there is no shop yet. Both plug in
// later without touching this file — see generator.go.
package bedwars

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

func init() {
	for _, m := range defaultModes {
		registerMode(m)
	}
}

// registerMode validates a preset, builds its arena once, and registers it
// with the matchmaker. Each mode is fully independent — adding "2 teams of
// 5" is one line in defaultModes; the layout, capacity, and queue bounds
// all follow from the Mode (see mode.go).
func registerMode(m Mode) {
	if err := m.validate(); err != nil {
		panic(err)
	}
	teams := buildTeams(m.Teams)

	// Prefer the mode's real map; fall back to the generated arena if it
	// can't be loaded or its bed count doesn't match (never fatal — the mode
	// still works, just on the procedural layout).
	var arena *Arena
	if m.Map != "" {
		if a, err := buildSchemArena(m.Map, teams); err != nil {
			slog.Warn("bedwars: using generated arena", "mode", m.ID, "map", m.Map, "err", err)
			arena = buildArena(teams)
		} else {
			arena = a
		}
	} else {
		arena = buildArena(teams)
	}

	game.Register(&game.Definition{
		ID:         m.ID,
		Name:       m.Name,
		MinPlayers: m.minPlayers(),
		MaxPlayers: m.maxPlayers(),
		Template:   arena.Template,
		New:        func() game.Logic { return newBedWars(arena, teams, m.TeamSize) },
	})
}

// bedWars is the per-instance round state and the game.Logic implementation.
// All mutable fields are guarded by mu; side effects on the instance
// (teleport, broadcast, EndGame) are performed after the lock is released
// to avoid re-entrancy surprises, mirroring the FFA reference game.
type bedWars struct {
	game.NoopLogic

	arena    *Arena
	granter  ResourceGranter
	teamSize int // max players per team (0 = unbounded)

	mu       sync.Mutex
	teams    []*teamState
	byEntity map[int32]int           // entityID → team ID
	placed   map[world.Position]bool // blocks players put down (breakable)
	engaged  bool                    // ≥2 teams have had a member (arm win-check)
	over     bool
}

func newBedWars(arena *Arena, teams []Team, teamSize int) *bedWars {
	g := &bedWars{
		arena:    arena,
		granter:  noopGranter{},
		teamSize: teamSize,
		teams:    make([]*teamState, len(teams)),
		byEntity: make(map[int32]int),
		placed:   make(map[world.Position]bool),
	}
	for i, t := range teams {
		g.teams[i] = newTeamState(t)
	}
	return g
}

// WithGranter swaps the resource granter (Open/Closed seam for the economy).
// Call it in the Definition's New factory once a real granter exists.
func (g *bedWars) WithGranter(r ResourceGranter) *bedWars {
	g.granter = r
	return g
}

func (g *bedWars) OnInstanceStart(ctx *game.Ctx) {
	ctx.Instance.BroadcastChat("", "BedWars! Protect your bed, break the others.")
}

// OnPlayerJoin assigns the newcomer to the smallest team, drops them on
// their island in Survival, and arms the win-check once two teams are live.
func (g *bedWars) OnPlayerJoin(ctx *game.Ctx, p game.PlayerHandle) {
	g.mu.Lock()
	ts := g.assignTeamLocked()
	ts.members[p.EntityID()] = true
	g.byEntity[p.EntityID()] = ts.team.ID
	if g.populatedTeams() >= 2 {
		g.engaged = true
	}
	spawn := g.arena.Spawns[ts.team.ID]
	teamName := ts.team.Name
	g.mu.Unlock()

	p.SetGamemode(player.Survival)
	teleport(p, spawn)
	ctx.Instance.BroadcastChat("", fmt.Sprintf("%s joined the %s team.", p.Name(), teamName))
}

// OnPlayerLeave drops the player from their team and re-checks the win
// condition (a disconnect can end the round).
func (g *bedWars) OnPlayerLeave(ctx *game.Ctx, p game.PlayerHandle) {
	g.mu.Lock()
	g.removeMemberLocked(p.EntityID())
	winner, decided := g.winnerLocked()
	g.mu.Unlock()

	if decided {
		g.finish(ctx, winner)
	}
}

// OnPlayerAttack treats one hit as a kill (the server has no HP system).
// Friendly fire is ignored.
func (g *bedWars) OnPlayerAttack(ctx *game.Ctx, attacker, target game.PlayerHandle) bool {
	g.mu.Lock()
	at, aok := g.byEntity[attacker.EntityID()]
	tt, tok := g.byEntity[target.EntityID()]
	sameTeam := aok && tok && at == tt
	g.mu.Unlock()
	if sameTeam {
		return true
	}
	g.kill(ctx, target, attacker.Name()+" killed "+target.Name())
	return true
}

// OnBlockBreak enforces the build rules:
//   - breaking an enemy bed kills it (and clears both halves);
//   - breaking your own bed is vetoed;
//   - breaking a block a player placed is allowed;
//   - breaking anything else (the map) is vetoed.
func (g *bedWars) OnBlockBreak(ctx *game.Ctx, p game.PlayerHandle, pos world.Position) bool {
	g.mu.Lock()
	if owner, isBed := g.arena.bedTeam(pos); isBed {
		breaker, ok := g.byEntity[p.EntityID()]
		if ok && breaker == owner {
			g.mu.Unlock()
			p.SendMessage("You cannot break your own bed.")
			return false
		}
		ts := g.teams[owner]
		alreadyGone := !ts.bedAlive
		ts.bedAlive = false
		beds := g.arena.BedBlocks[owner]
		teamName := ts.team.Name
		g.mu.Unlock()

		if alreadyGone {
			return true
		}
		// Clear both bed halves (the engine only air-fills the one the
		// client dug; we remove the partner so no stray half lingers).
		for _, bp := range beds {
			ctx.Instance.SetBlock(bp, world.Air)
		}
		ctx.Instance.BroadcastChat("",
			fmt.Sprintf("%s's bed was destroyed by %s!", teamName, p.Name()))
		return true
	}

	if g.placed[pos] {
		delete(g.placed, pos)
		g.mu.Unlock()
		return true
	}
	g.mu.Unlock()
	return false // protect the original map
}

// OnBlockPlace records the position so it becomes breakable later. The
// engine always reports world.Stone (no inventory yet); the type is
// irrelevant to the placed-block bookkeeping.
func (g *bedWars) OnBlockPlace(_ *game.Ctx, _ game.PlayerHandle, pos world.Position, _ world.Block) bool {
	g.mu.Lock()
	g.placed[pos] = true
	g.mu.Unlock()
	return true
}

// OnTick drives void-death detection and resource generators.
func (g *bedWars) OnTick(ctx *game.Ctx, tick uint64) {
	if tick%voidScanInterval == 0 {
		g.checkVoid(ctx)
	}
	g.runGenerators(ctx, tick)
}

// --- internals -------------------------------------------------------------

// kill respawns the victim at their island if their bed is alive, otherwise
// eliminates them. Announces reason. Re-checks the win condition.
func (g *bedWars) kill(ctx *game.Ctx, victim game.PlayerHandle, reason string) {
	g.mu.Lock()
	teamID, ok := g.byEntity[victim.EntityID()]
	if !ok || g.over {
		g.mu.Unlock()
		return
	}
	ts := g.teams[teamID]
	bedAlive := ts.bedAlive
	spawn := g.arena.Spawns[teamID]
	if !bedAlive {
		g.removeMemberLocked(victim.EntityID())
	}
	winner, decided := g.winnerLocked()
	g.mu.Unlock()

	ctx.Instance.BroadcastChat("", reason)
	if bedAlive {
		teleport(victim, spawn) // respawn
	} else {
		victim.SetGamemode(player.Spectator)
		victim.Teleport(spectatorPos.X, spectatorPos.Y, spectatorPos.Z)
		ctx.Instance.BroadcastChat("", victim.Name()+" was eliminated.")
	}
	if decided {
		g.finish(ctx, winner)
	}
}

// checkVoid kills any active (non-spectator) player who has fallen below
// voidY. Runs off the tick loop since the engine has no void detection.
func (g *bedWars) checkVoid(ctx *game.Ctx) {
	for _, p := range ctx.Instance.Players() {
		if p.Pose().Gamemode == player.Spectator {
			continue
		}
		g.mu.Lock()
		_, tracked := g.byEntity[p.EntityID()]
		g.mu.Unlock()
		if tracked && p.Pose().Y < voidY {
			g.kill(ctx, p, p.Name()+" fell into the void.")
		}
	}
}

// runGenerators fires each generator on its interval, handing the recipient
// set to the (pluggable) granter.
func (g *bedWars) runGenerators(ctx *game.Ctx, tick uint64) {
	for _, gen := range g.arena.Generators {
		if gen.IntervalTicks == 0 || tick%gen.IntervalTicks != 0 {
			continue
		}
		g.granter.Grant(ctx, gen, g.recipientsFor(ctx, gen))
	}
}

// recipientsFor returns the players a generator's output should target:
// living members of the owning team, or everyone for a neutral generator.
func (g *bedWars) recipientsFor(ctx *game.Ctx, gen Generator) []game.PlayerHandle {
	all := ctx.Instance.Players()
	if gen.TeamID == neutral {
		return all
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]game.PlayerHandle, 0, len(all))
	for _, p := range all {
		if g.byEntity[p.EntityID()] == gen.TeamID {
			out = append(out, p)
		}
	}
	return out
}

// finish announces the winner and schedules teardown. Idempotent via over.
func (g *bedWars) finish(ctx *game.Ctx, winner *teamState) {
	g.mu.Lock()
	if g.over {
		g.mu.Unlock()
		return
	}
	g.over = true
	g.mu.Unlock()

	if winner != nil {
		ctx.Instance.BroadcastChat("",
			fmt.Sprintf("*** %s team wins! Returning to hub in %ds ***",
				winner.team.Name, int(endDelay/time.Second)))
	} else {
		ctx.Instance.BroadcastChat("", "*** Draw! Returning to hub… ***")
	}
	inst := ctx.Instance
	go func() {
		time.Sleep(endDelay)
		inst.EndGame()
	}()
}

// --- locked helpers (caller holds g.mu) ------------------------------------

func (g *bedWars) removeMemberLocked(eid int32) {
	if teamID, ok := g.byEntity[eid]; ok {
		delete(g.teams[teamID].members, eid)
		delete(g.byEntity, eid)
	}
}

// assignTeamLocked picks the team a newcomer should join: the one with the
// fewest members that still has room (ties go to the lowest ID), so joins
// stay balanced and never exceed teamSize. If every team is full (shouldn't
// happen — the matchmaker caps the lobby at Teams×TeamSize) it falls back to
// the globally smallest team so a player is never dropped on the floor.
func (g *bedWars) assignTeamLocked() *teamState {
	var best, fallback *teamState
	for _, ts := range g.teams {
		if fallback == nil || len(ts.members) < len(fallback.members) {
			fallback = ts
		}
		if g.teamSize > 0 && len(ts.members) >= g.teamSize {
			continue // full
		}
		if best == nil || len(ts.members) < len(best.members) {
			best = ts
		}
	}
	if best != nil {
		return best
	}
	return fallback
}

func (g *bedWars) populatedTeams() int {
	n := 0
	for _, ts := range g.teams {
		if len(ts.members) > 0 {
			n++
		}
	}
	return n
}

// winnerLocked decides the round: once the game has engaged (≥2 teams were
// populated), if exactly one team remains in play it's the winner; if none
// remain it's a draw. Returns decided=false while the round continues.
func (g *bedWars) winnerLocked() (winner *teamState, decided bool) {
	if !g.engaged || g.over {
		return nil, false
	}
	var live []*teamState
	for _, ts := range g.teams {
		if ts.inPlay() {
			live = append(live, ts)
		}
	}
	switch len(live) {
	case 1:
		return live[0], true
	case 0:
		return nil, true // everyone gone simultaneously → draw
	default:
		return nil, false
	}
}

// teleport places p at a spawn point, centred in the block.
func teleport(p game.PlayerHandle, sp world.SpawnPoint) {
	p.Teleport(float64(sp.Position.X)+0.5, float64(sp.Position.Y), float64(sp.Position.Z)+0.5)
}
