// Package ffa is a minimal Free-For-All mini-game. It's the reference
// implementation that proves the plugin API in game/ actually lets a
// game live entirely outside the server package.
//
// Rules:
//   - 16×16 stone platform at Y=64, 4 fixed spawn points at the corners.
//   - Each player spawns at a random corner on join.
//   - Real 1.9 combat (the instance's core PvP system): players trade
//     health-based hits. A kill (Logic.OnPlayerDeath) = +1 to the killer;
//     the victim respawns instantly at a random corner (no death screen).
//   - First to winScore kills wins; instance announces, schedules a
//     5-second tear-down via Ctx.Instance.EndGame.
//
// Limitations the simplicity papers over: no inventory/kit (players fight
// with the configured default weapon), no fall-off-platform detection.
// Good enough as a pipeline smoke test for the engine.
package ffa

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"minecraft-server/game"
	"minecraft-server/world"
)

const (
	platformY = 64 // top surface of the platform sits at Y=platformY (blocks fill Y=platformY)
	halfSize  = 8  // → 16×16 platform footprint
	winScore  = 3
	endDelay  = 5 * time.Second
)

// spawns: 4 corners, one block above the platform top so feet land on stone.
var spawns = []world.Position{
	{X: -halfSize + 1, Y: platformY + 1, Z: -halfSize + 1},
	{X: halfSize - 2, Y: platformY + 1, Z: -halfSize + 1},
	{X: -halfSize + 1, Y: platformY + 1, Z: halfSize - 2},
	{X: halfSize - 2, Y: platformY + 1, Z: halfSize - 2},
}

func init() {
	game.Register(&game.Definition{
		ID:         "ffa",
		Name:       "Free-For-All",
		MinPlayers: 2,
		MaxPlayers: 8,
		Template:   buildTemplate(),
		New:        func() game.Logic { return &ffaLogic{scores: map[int32]int{}} },
	})
}

func buildTemplate() *world.Template {
	t := world.NewTemplate()
	for x := -halfSize; x < halfSize; x++ {
		for z := -halfSize; z < halfSize; z++ {
			t.SetBlock(world.Position{X: x, Y: platformY, Z: z}, world.Stone)
		}
	}
	return t
}

// ffaLogic is the per-instance state. NoopLogic supplies defaults for the
// hooks we don't care about (block break/place, chat, tick).
type ffaLogic struct {
	game.NoopLogic

	mu     sync.Mutex
	scores map[int32]int // entityID → kill count
	over   bool          // set when a winner is announced; subsequent kills are ignored
}

func (g *ffaLogic) OnInstanceStart(ctx *game.Ctx) {
	// Arena combat: keep the default PvP on, but respawn instantly at a
	// corner instead of showing the vanilla death screen.
	ctx.Instance.SetPvP(true)
	ctx.Instance.SetInstantRespawn(true)
	ctx.Instance.BroadcastChat("",
		fmt.Sprintf("Free-For-All! First to %d kills wins.", winScore))
}

func (g *ffaLogic) OnPlayerJoin(ctx *game.Ctx, p game.PlayerHandle) {
	g.spawnPlayer(p)
	g.mu.Lock()
	g.scores[p.EntityID()] = 0
	g.mu.Unlock()
	ctx.Instance.BroadcastChat("", p.Name()+" entered the arena.")
}

func (g *ffaLogic) OnPlayerLeave(ctx *game.Ctx, p game.PlayerHandle) {
	g.mu.Lock()
	delete(g.scores, p.EntityID())
	g.mu.Unlock()
	ctx.Instance.BroadcastChat("", p.Name()+" left.")
}

// OnPlayerAttack lets every hit through to the core combat system once the
// game is live. Returning false would veto the hit; we only do that after a
// winner is decided, to freeze the arena.
func (g *ffaLogic) OnPlayerAttack(ctx *game.Ctx, attacker, target game.PlayerHandle) bool {
	g.mu.Lock()
	over := g.over
	g.mu.Unlock()
	return !over
}

// OnPlayerDeath scores the kill. The server already healed and respawned the
// victim at the instance spawn (instant respawn) before this fires; we just
// move them to a random corner and tally the killer's point.
func (g *ffaLogic) OnPlayerDeath(ctx *game.Ctx, victim, killer game.PlayerHandle) {
	// Re-place the victim at a random corner (overrides the default spawn).
	g.spawnPlayer(victim)

	if killer == nil {
		return // environmental death — no one to credit
	}

	g.mu.Lock()
	if g.over {
		g.mu.Unlock()
		return
	}
	g.scores[killer.EntityID()]++
	score := g.scores[killer.EntityID()]
	won := score >= winScore
	if won {
		g.over = true
	}
	g.mu.Unlock()

	ctx.Instance.BroadcastChat("",
		fmt.Sprintf("%s ▶ %s  (%d/%d)", killer.Name(), victim.Name(), score, winScore))

	if won {
		ctx.Instance.BroadcastChat("",
			fmt.Sprintf("*** %s wins! Returning to hub in %ds ***",
				killer.Name(), int(endDelay/time.Second)))
		// EndGame teleports everyone back to hub and removes the instance.
		// Run it outside our hook so we don't block the death handler.
		go func() {
			time.Sleep(endDelay)
			ctx.Instance.EndGame()
		}()
	}
}

// spawnPlayer teleports p to a random spawn corner. Centers them in the
// block (0.5 offsets) so the client doesn't show them clipping a corner.
func (g *ffaLogic) spawnPlayer(p game.PlayerHandle) {
	sp := spawns[rand.Intn(len(spawns))]
	p.Teleport(float64(sp.X)+0.5, float64(sp.Y), float64(sp.Z)+0.5)
}
