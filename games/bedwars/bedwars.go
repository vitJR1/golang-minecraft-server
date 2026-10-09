// Package bedwars is a 4-team BedWars mini-game.
//
// Rules (Hypixel-style):
//   - Teams, each on its own island with a bed. Joining players are
//     balanced across teams and equipped with their kit (kit.go): wooden
//     sword, team-coloured leather armour, owned armour tier and tools.
//   - Break an enemy team's bed (OnBlockBreak) and that team can no longer
//     respawn. You cannot break your own bed, the map, or anything you
//     didn't place — only player-placed blocks and beds are breakable.
//   - Real HP combat (weapon damage + armour). Death (OnPlayerDeath, with
//     the server's custom-respawn mode) drops the victim's resources to the
//     killer, downgrades tools, and either respawns them at their island
//     after respawnDelayTicks (bed alive) or eliminates them (→ Spectator,
//     "final kill"). Falling into the void kills too.
//   - Last team with a living member wins; the instance then returns
//     everyone to the hub.
//
// Resource generators tick on a schedule and hand off to a ResourceGranter
// (default dropGranter, which spawns the resource as a dropped-item entity
// at the generator block for players to walk over). Resources are spent at
// the item shop (shop.go) and the team-upgrade shop (upgrades.go).
package bedwars

import (
	"fmt"
	"log/slog"
	"strings"
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
	kits     map[int32]*kit           // entityID → persistent loadout
	pending  map[int32]pendingRespawn // dead players waiting to respawn
	tick     uint64                   // last OnTick value
	ctx      *game.Ctx                // set in OnInstanceStart

	// holograms are the countdown labels over diamond/emerald generators
	// (see hologram.go); spawned in OnInstanceStart, refreshed on tick.
	holograms []genHologram
}

func newBedWars(arena *Arena, teams []Team, teamSize int) *bedWars {
	g := &bedWars{
		arena:    arena,
		granter:  dropGranter{},
		teamSize: teamSize,
		teams:    make([]*teamState, len(teams)),
		byEntity: make(map[int32]int),
		placed:   make(map[world.Position]bool),
		kits:     make(map[int32]*kit),
		pending:  make(map[int32]pendingRespawn),
	}
	for i, t := range teams {
		g.teams[i] = newTeamState(t)
	}
	return g
}

// WithGranter swaps the resource granter (Open/Closed seam for the economy).
// The constructor defaults to dropGranter (items appear at the forge);
// override for tests or a custom economy (inventoryGranter auto-collects,
// noopGranter is silent).
func (g *bedWars) WithGranter(r ResourceGranter) *bedWars {
	g.granter = r
	return g
}

func (g *bedWars) OnInstanceStart(ctx *game.Ctx) {
	g.mu.Lock()
	g.ctx = ctx
	g.mu.Unlock()
	// Hypixel rules: real HP from weapons/armour, the game drives respawns,
	// placed TNT lights itself.
	ctx.Instance.SetPvP(true)
	ctx.Instance.SetWeaponDamage(true)
	ctx.Instance.SetCustomRespawn(true)
	ctx.Instance.SetTNTAutoPrime(true)
	ctx.Instance.BroadcastChat("", "BedWars! Protect your bed, break the others.")
	g.spawnHolograms(ctx)
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
	k := g.kitOfLocked(p.EntityID())
	g.mu.Unlock()

	p.SetGamemode(player.Survival)
	teleport(p, spawn)
	g.equip(p, ts, k)
	ctx.Instance.BroadcastChat("", fmt.Sprintf("%s joined the %s team.", p.Name(), teamName))
}

// OnPlayerLeave drops the player from their team and re-checks the win
// condition (a disconnect can end the round).
func (g *bedWars) OnPlayerLeave(ctx *game.Ctx, p game.PlayerHandle) {
	g.mu.Lock()
	g.removeMemberLocked(p.EntityID())
	delete(g.pending, p.EntityID())
	winner, decided := g.winnerLocked()
	g.mu.Unlock()

	if decided {
		g.finish(ctx, winner)
	}
}

// OnPlayerAttack vetoes friendly fire; enemy hits go to the server's HP
// combat (weapon damage, armour), and a lethal one comes back as
// OnPlayerDeath.
func (g *bedWars) OnPlayerAttack(_ *game.Ctx, attacker, target game.PlayerHandle) bool {
	g.mu.Lock()
	at, aok := g.byEntity[attacker.EntityID()]
	tt, tok := g.byEntity[target.EntityID()]
	sameTeam := aok && tok && at == tt
	g.mu.Unlock()
	return !sameTeam
}

// OnPlayerDeath applies the Hypixel death rules: the killer loots the
// victim's resources, tools drop a tier, the inventory is wiped, and the
// victim either waits respawnDelayTicks as a spectator (bed alive) or is
// eliminated for good.
func (g *bedWars) OnPlayerDeath(ctx *game.Ctx, victim, killer game.PlayerHandle) {
	g.mu.Lock()
	teamID, ok := g.byEntity[victim.EntityID()]
	if !ok || g.over {
		g.mu.Unlock()
		return
	}
	ts := g.teams[teamID]
	bedAlive := ts.bedAlive
	k := g.kitOfLocked(victim.EntityID())
	k.onDeath()
	killerTeam, killerKnown := -1, false
	if killer != nil {
		killerTeam, killerKnown = g.byEntity[killer.EntityID()]
	}
	enemyKiller := killerKnown && killerTeam != teamID
	if bedAlive {
		g.pending[victim.EntityID()] = pendingRespawn{p: victim, at: g.tick + respawnDelayTicks}
	} else {
		g.removeMemberLocked(victim.EntityID())
	}
	winner, decided := g.winnerLocked()
	teamName := ts.team.Name
	g.mu.Unlock()

	// Loot: the killer picks up the victim's currencies.
	loot := lootResources(victim)
	if enemyKiller {
		for _, r := range []Resource{Iron, Gold, Diamond, Emerald} {
			if n := loot[r]; n > 0 {
				killer.GiveItem(r.item(), n)
				killer.SendMessage(fmt.Sprintf("%s+%d %s", r.colour(), n, r.String()))
			}
		}
	}
	victim.ClearInventory()
	for _, e := range []string{"haste", "speed", "jump_boost", "invisibility", "regeneration"} {
		victim.RemoveEffect(e)
	}

	msg := victim.Name() + " died."
	if killer != nil {
		msg = victim.Name() + " was killed by " + killer.Name() + "."
	}
	if !bedAlive {
		msg += " §c§lFINAL KILL!"
	}
	ctx.Instance.BroadcastChat("", msg)

	pose := victim.Pose()
	victim.SetGamemode(player.Spectator)
	if bedAlive {
		victim.Teleport(pose.X, pose.Y+5, pose.Z)
		victim.SendTitle("§cYOU DIED!", fmt.Sprintf("§eYou will respawn in §c%d §eseconds!", respawnDelayTicks/20), 0, 40, 10)
	} else {
		victim.Teleport(spectatorPos.X, spectatorPos.Y, spectatorPos.Z)
		victim.SendTitle("§cYOU DIED!", "§7You will not respawn.", 0, 60, 20)
		ctx.Instance.BroadcastChat("", fmt.Sprintf("%s was eliminated (%s team).", victim.Name(), teamName))
	}
	if decided {
		g.finish(ctx, winner)
	}
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

// RewritePlacedBlock (game.PlacementRewriter) turns any bed a player places
// into their team's colour, so a bought or found bed always matches.
func (g *bedWars) RewritePlacedBlock(_ *game.Ctx, p game.PlayerHandle, _ world.Position, blk world.Block) world.Block {
	if !strings.HasSuffix(blk.Name, "_bed") {
		return blk
	}
	team, ok := g.teamOf(p)
	if !ok {
		return blk
	}
	return team.Bed
}

// OnTick drives void-death detection, pending respawns, resource
// generators, and the generator countdown holograms.
func (g *bedWars) OnTick(ctx *game.Ctx, tick uint64) {
	g.mu.Lock()
	g.tick = tick
	g.mu.Unlock()
	if tick%voidScanInterval == 0 {
		g.checkVoid(ctx)
	}
	g.processRespawns(ctx, tick)
	g.runGenerators(ctx, tick)
	g.updateHolograms(ctx, tick)
}

// --- internals -------------------------------------------------------------

// pendingRespawn is a dead player waiting for their respawn tick.
type pendingRespawn struct {
	p  game.PlayerHandle
	at uint64
}

// processRespawns counts down titles for the dead and brings them back at
// their island with a fresh kit when their time is up.
func (g *bedWars) processRespawns(ctx *game.Ctx, tick uint64) {
	g.mu.Lock()
	if len(g.pending) == 0 {
		g.mu.Unlock()
		return
	}
	type due struct {
		p    game.PlayerHandle
		ts   *teamState
		k    *kit
		spwn world.SpawnPoint
	}
	var ready []due
	var waiting []pendingRespawn
	for eid, pr := range g.pending {
		if tick >= pr.at {
			teamID, ok := g.byEntity[eid]
			delete(g.pending, eid)
			if !ok {
				continue
			}
			ready = append(ready, due{p: pr.p, ts: g.teams[teamID], k: g.kitOfLocked(eid), spwn: g.arena.Spawns[teamID]})
		} else if (pr.at-tick)%20 == 0 {
			waiting = append(waiting, pr)
		}
	}
	g.mu.Unlock()

	for _, pr := range waiting {
		secs := (pr.at - tick) / 20
		pr.p.SendTitle("§cYOU DIED!", fmt.Sprintf("§eYou will respawn in §c%d §eseconds!", secs), 0, 25, 5)
	}
	for _, d := range ready {
		d.p.Respawn(float64(d.spwn.Position.X)+0.5, float64(d.spwn.Position.Y), float64(d.spwn.Position.Z)+0.5)
		d.p.SetGamemode(player.Survival)
		g.equip(d.p, d.ts, d.k)
		d.p.SendTitle("§aRESPAWNED!", "", 0, 20, 10)
	}
	_ = ctx
}

// checkVoid kills any living, active player who has fallen below voidY. The
// death itself comes back through OnPlayerDeath.
func (g *bedWars) checkVoid(ctx *game.Ctx) {
	for _, p := range ctx.Instance.Players() {
		pose := p.Pose()
		if pose.Gamemode == player.Spectator || pose.Dead {
			continue
		}
		g.mu.Lock()
		_, tracked := g.byEntity[p.EntityID()]
		g.mu.Unlock()
		if tracked && pose.Y < voidY {
			p.Kill()
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
