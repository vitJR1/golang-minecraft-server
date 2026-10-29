package server

import (
	"math/rand"
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// duelGuide puts each bot on the team it's added to; the two teams fight.
type duelGuide struct{ team map[int32]string }

func (d *duelGuide) BotJoin(_ *game.Ctx, p game.PlayerHandle, team string) error {
	d.team[p.EntityID()] = team
	return nil
}
func (d *duelGuide) BotEnemy(_ *game.Ctx, p, o game.PlayerHandle) bool {
	a, ok1 := d.team[p.EntityID()]
	b, ok2 := d.team[o.EntityID()]
	return ok1 && ok2 && a != b && !o.Pose().Dead
}
func (d *duelGuide) BotPlan(*game.Ctx, game.PlayerHandle) game.BotPlan { return game.BotPlan{} }
func (d *duelGuide) BotBuy(*game.Ctx, game.PlayerHandle, string) bool  { return false }

// botDuel runs one fight of two equal bots on a flat arena; it returns which
// one (0 = the first added) died or fell off first, or -1 when nobody did.
func botDuel(t *testing.T, diff Difficulty) (int, uint64) {
	t.Helper()
	inst := bareInstance(New(), world.NewMemoryWorld())
	for x := -12; x <= 12; x++ {
		for z := -12; z <= 12; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	inst.Combat = DefaultCombatConfig()
	inst.SetPvP(true)
	inst.SetCustomRespawn(true) // the dead stay dead
	inst.botGuide = &duelGuide{team: map[int32]string{}}
	var bots [2]*ClientConnection
	for i, team := range []string{"red", "blue"} {
		b, err := inst.Server.AddBot(inst, team, diff)
		if err != nil {
			t.Fatal(err)
		}
		x := -4.5 + 9*float64(i)
		b.player.MoveTo(x, 64, 0.5, true)
		b.player.SetGamemode(player.Survival)
		bots[i] = b
	}
	for tick := uint64(1); tick <= 2400; tick++ {
		// Not runTicks: a duel never needs a far route, and a bot knocked
		// off the arena starts a long (failing) one runTicks would wait for.
		inst.botTick(inst.tickCount.Add(1))
		for i, b := range bots {
			if s := b.player.Snapshot(); s.Dead || s.Y < 40 { // knocked into the void
				return i, tick
			}
		}
	}
	return -1, 2400
}

// TestBotDuelsAreFair: two equal bots facing each other win about equally
// often — neither the order they joined in nor anything else decides it.
// (Before bots ticked in a shuffled order, the first-added won ≈ 3 to 1.)
func TestBotDuelsAreFair(t *testing.T) {
	const n = 100
	wins := [2]int{}
	for k := 0; k < n; k++ {
		if loser, _ := botDuel(t, DifficultyHard); loser >= 0 {
			wins[1-loser]++
		}
	}
	if wins[0] < 30 || wins[1] < 30 {
		t.Fatalf("wins %v over %d duels: one-sided", wins, n)
	}
}

// TestBotReaction: swings wait a human 30–100 ms — in ticks 0..2, about
// 1.3 on average.
func TestBotReaction(t *testing.T) {
	b := &bot{rng: rand.New(rand.NewSource(1))}
	sum := 0.0
	const n = 20000
	for k := 0; k < n; k++ {
		r := b.reactTicks()
		if r > 2 {
			t.Fatalf("reaction of %d ticks (> 100 ms)", r)
		}
		sum += float64(r)
	}
	if mean := sum / n * 50; mean < 60 || mean > 70 {
		t.Fatalf("mean reaction %.1f ms, want ≈ 65", mean)
	}
}
