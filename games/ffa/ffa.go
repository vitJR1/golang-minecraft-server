// Package ffa is a minimal Free-For-All mini-game. It's the reference
// implementation that proves the plugin API in game/ actually lets a
// game live entirely outside the server package.
//
// Rules:
//   - 16×16 stone platform at Y=64, 4 fixed spawn points at the corners.
//   - Each player spawns at a random corner on join.
//   - "Attack" SbPlayInteract (Logic.OnPlayerAttack) = +1 to attacker,
//     victim respawns at a random corner. No actual damage system —
//     the attack hook IS the kill event.
//   - First to winScore points wins; instance announces, schedules a
//     5-second tear-down via Ctx.Instance.EndGame.
//
// Limitations the simplicity papers over: no inventory/kit (players keep
// the default empty Creative kit), no fall-off-platform detection, no
// hit cooldown — a spam-clicker would farm points. Good enough as a
// pipeline smoke test for the engine.
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

func (g *ffaLogic) OnPlayerAttack(ctx *game.Ctx, attacker, target game.PlayerHandle) bool {
	g.mu.Lock()
	if g.over {
		g.mu.Unlock()
		return true
	}
	g.scores[attacker.EntityID()]++
	score := g.scores[attacker.EntityID()]
	won := score >= winScore
	if won {
		g.over = true
	}
	g.mu.Unlock()

	ctx.Instance.BroadcastChat("",
		fmt.Sprintf("%s ▶ %s  (%d/%d)", attacker.Name(), target.Name(), score, winScore))

	// "Death": respawn the victim at a random corner.
	g.spawnPlayer(target)

	if won {
		ctx.Instance.BroadcastChat("",
			fmt.Sprintf("*** %s wins! Returning to hub in %ds ***",
				attacker.Name(), int(endDelay/time.Second)))
		// EndGame teleports everyone back to hub and removes the instance.
		// Run it outside our hook so we don't block the attack handler.
		go func() {
			time.Sleep(endDelay)
			ctx.Instance.EndGame()
		}()
	}
	return true
}

// spawnPlayer teleports p to a random spawn corner. Centers them in the
// block (0.5 offsets) so the client doesn't show them clipping a corner.
func (g *ffaLogic) spawnPlayer(p game.PlayerHandle) {
	sp := spawns[rand.Intn(len(spawns))]
	p.Teleport(float64(sp.X)+0.5, float64(sp.Y), float64(sp.Z)+0.5)
}
