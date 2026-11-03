package server

import (
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/server/bots/botai"
	"minecraft-server/server/gameplay/combat"
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
func botDuel(t *testing.T, diff botai.Difficulty) (int, uint64) {
	t.Helper()
	inst := bareInstance(New(), world.NewMemoryWorld())
	for x := -12; x <= 12; x++ {
		for z := -12; z <= 12; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
		}
	}
	inst.Combat = combat.DefaultConfig()
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
		inst.botTick(inst.ticks.Advance(1))
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
		if loser, _ := botDuel(t, botai.DifficultyHard); loser >= 0 {
			wins[1-loser]++
		}
	}
	if wins[0] < 30 || wins[1] < 30 {
		t.Fatalf("wins %v over %d duels: one-sided", wins, n)
	}
}
