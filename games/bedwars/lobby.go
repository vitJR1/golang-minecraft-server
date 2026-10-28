package bedwars

import (
	"errors"
	"fmt"
	"sort"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// lobby.go: the waiting phase before a round.
//
// An arena built with WithMinPlayers(n > 0) starts in the lobby: joiners are
// parked at the map's centre (Arena.Lobby) in Adventure mode with an empty
// inventory, nobody has a team yet, PvP / generators / bed rules are off.
// Once n players are waiting a countdown runs (lobbyCountdownTicks, with a
// title every second); if someone leaves and the count drops under n it is
// cancelled. When it ends startRound deals the waiting players onto the
// teams (smallest first, so 8 players on a 4×4 map make two full teams),
// drops everyone on their island with a kit, arms PvP and the generator
// timeline (startTick), and the ordinary round logic takes over. Players
// who join a running round are placed straight onto a team, as before.
//
// Without WithMinPlayers (tests, legacy callers) there is no lobby: the
// round is "started" from the first tick and joins go straight to a team.

const (
	// lobbyCountdownTicks is how long the start countdown runs once enough
	// players are waiting.
	lobbyCountdownTicks = 10 * 20
)

// WithMinPlayers makes the round wait in the lobby until n players are in
// (n ≤ 0 keeps the no-lobby behaviour).
func (g *bedWars) WithMinPlayers(n int) *bedWars {
	g.mu.Lock()
	g.minPlayers = n
	g.started = n <= 0
	g.mu.Unlock()
	return g
}

// joinLobbyLocked parks a newcomer in the waiting set and starts the
// countdown when the room fills. Caller holds g.mu; returns what to tell
// the players once the lock is dropped.
func (g *bedWars) joinLobbyLocked(p game.PlayerHandle) (waiting, need int, countdownStarted bool) {
	g.waiting[p.EntityID()] = p
	waiting, need = len(g.waiting), g.minPlayers
	if waiting >= need && g.countdownAt == 0 {
		g.countdownAt = g.tick + lobbyCountdownTicks
		countdownStarted = true
	}
	return waiting, need, countdownStarted
}

// joinLobby is the lobby half of OnPlayerJoin.
func (g *bedWars) joinLobby(ctx *game.Ctx, p game.PlayerHandle) {
	g.mu.Lock()
	waiting, need, countdownStarted := g.joinLobbyLocked(p)
	lobby := g.arena.Lobby
	tick := g.tick
	g.mu.Unlock()

	p.ClearInventory()
	p.SetGamemode(player.Adventure)
	teleport(p, lobby)
	p.SetSidebar(sidebarTitle, g.sidebarLines(p, tick))
	ctx.Instance.BroadcastChat("", fmt.Sprintf("§e%s §7joined (§a%d§7/§a%d§7).", p.Name(), waiting, need))
	if countdownStarted {
		ctx.Instance.BroadcastChat("", fmt.Sprintf("§aEnough players! The game starts in §e%d §aseconds.", lobbyCountdownTicks/20))
	}
}

// leaveLobby is the lobby half of OnPlayerLeave: drops the player and
// cancels a countdown the room no longer qualifies for.
func (g *bedWars) leaveLobby(ctx *game.Ctx, p game.PlayerHandle) {
	g.mu.Lock()
	delete(g.waiting, p.EntityID())
	waiting, need := len(g.waiting), g.minPlayers
	cancelled := false
	if g.countdownAt != 0 && waiting < need && !g.forced {
		g.countdownAt = 0
		cancelled = true
	}
	g.mu.Unlock()

	ctx.Instance.BroadcastChat("", fmt.Sprintf("§e%s §7left (§a%d§7/§a%d§7).", p.Name(), waiting, need))
	if cancelled {
		ctx.Instance.BroadcastChat("", "§cNot enough players, the countdown was cancelled.")
	}
}

// ForceStart (game.ForceStarter, /forcestart) starts the round on the next
// tick with whoever is waiting, skipping minPlayers and the countdown. The
// start itself stays on the tick loop (lobbyTick → startRound), like a
// countdown that ran out.
func (g *bedWars) ForceStart(*game.Ctx) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case g.started:
		return errors.New("the round is already running")
	case len(g.waiting) == 0:
		return errors.New("nobody is waiting")
	}
	g.countdownAt = g.tick + 1
	g.forced = true
	return nil
}

// lobbyTick runs the waiting phase: countdown titles, the start itself,
// and keeping players on the lobby platform (a fall off the centre island
// is a teleport back, not a death).
func (g *bedWars) lobbyTick(ctx *game.Ctx, tick uint64) {
	g.mu.Lock()
	at := g.countdownAt
	lobby := g.arena.Lobby
	g.mu.Unlock()

	if at != 0 && tick >= at {
		g.startRound(ctx, tick)
		return
	}
	players := ctx.Instance.Players()
	if at != 0 && (at-tick)%20 == 0 {
		secs := (at - tick) / 20
		for _, p := range players {
			p.SendTitle(fmt.Sprintf("§e%d", secs), "§7Get ready!", 0, 20, 5)
			p.PlaySound("minecraft:block.note_block.hat", 1, 1)
		}
	}
	if tick%voidScanInterval == 0 {
		for _, p := range players {
			if p.Pose().Y < voidY {
				teleport(p, lobby)
			}
		}
	}
	if tick%20 == 0 {
		g.refreshSidebars(ctx, tick)
	}
}

// startRound ends the lobby: deals the waiting players onto teams (fewest
// members first, lowest entity id first for determinism), equips and
// places them, arms PvP and the generator timeline.
func (g *bedWars) startRound(ctx *game.Ctx, tick uint64) {
	type placement struct {
		p    game.PlayerHandle
		ts   *teamState
		k    *kit
		spwn world.SpawnPoint
	}
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return
	}
	g.started = true
	g.startTick = tick
	g.countdownAt = 0
	eids := make([]int32, 0, len(g.waiting))
	for eid := range g.waiting {
		eids = append(eids, eid)
	}
	sort.Slice(eids, func(i, j int) bool { return eids[i] < eids[j] })
	placements := make([]placement, 0, len(eids))
	for _, eid := range eids {
		ts := g.assignTeamLocked()
		ts.members[eid] = true
		g.byEntity[eid] = ts.team.ID
		placements = append(placements, placement{p: g.waiting[eid], ts: ts, k: g.kitOfLocked(eid), spwn: g.arena.Spawns[ts.team.ID]})
	}
	g.waiting = make(map[int32]game.PlayerHandle)
	if g.populatedTeams() >= 2 {
		g.engaged = true
	}
	g.mu.Unlock()

	ctx.Instance.SetPvP(true)
	for _, pl := range placements {
		pl.p.SetGamemode(player.Survival)
		teleport(pl.p, pl.spwn)
		g.equip(pl.p, pl.ts, pl.k)
		pl.p.SendMessage(fmt.Sprintf("You are on the §e%s §fteam.", pl.ts.team.Name))
		pl.p.SendTitle("§a§lGO!", "§eProtect your bed, break the others!", 0, 40, 10)
		pl.p.PlaySound("minecraft:entity.ender_dragon.growl", 1, 1)
	}
	ctx.Instance.BroadcastChat("", "§a§lThe game has started!")
	g.refreshSidebars(ctx, tick)
}

// lobbySidebarLines is the sidebar while waiting.
func (g *bedWars) lobbySidebarLines(tick uint64) []string {
	g.mu.Lock()
	waiting, need, at := len(g.waiting), g.minPlayers, g.countdownAt
	g.mu.Unlock()
	status := "§7Waiting for players…"
	if at != 0 && at > tick {
		status = fmt.Sprintf("§fStarting in §a%ds", (at-tick+19)/20)
	}
	return []string{
		fmt.Sprintf("§fPlayers: §a%d§f/§a%d", waiting, need),
		status,
	}
}

// defaultLobby picks where waiting players stand: the shared diamond
// generator (the map's centre island) if there is one, else the first
// neutral generator, else the centroid of the team spawns at their
// highest Y.
func defaultLobby(gens []Generator, spawns []world.SpawnPoint) world.SpawnPoint {
	var first *Generator
	for i := range gens {
		gen := &gens[i]
		if gen.TeamID != neutral {
			continue
		}
		if gen.Resource == Diamond {
			return world.SpawnPoint{Position: gen.Pos}
		}
		if first == nil {
			first = gen
		}
	}
	if first != nil {
		return world.SpawnPoint{Position: first.Pos}
	}
	if len(spawns) == 0 {
		return world.SpawnPoint{}
	}
	var sx, sz, y int
	for _, sp := range spawns {
		sx += sp.Position.X
		sz += sp.Position.Z
		y = max(y, sp.Position.Y)
	}
	return world.SpawnPoint{Position: world.Position{X: sx / len(spawns), Y: y, Z: sz / len(spawns)}}
}
