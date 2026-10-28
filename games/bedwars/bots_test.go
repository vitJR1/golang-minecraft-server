package bedwars

import (
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
)

func TestBotJoinPicksTheTeamInTheLobby(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8)
	bot := join(g, inst, ctx, "BOT_Steve", 7)
	if err := g.BotJoin(ctx, bot, "green"); err != nil {
		t.Fatal(err)
	}
	if err := g.BotJoin(ctx, bot, "purple"); err == nil {
		t.Fatal("an unknown team was accepted")
	}
	if err := g.ForceStart(ctx); err != nil {
		t.Fatal(err)
	}
	g.OnTick(ctx, 1)
	if tm, ok := g.teamOf(bot); !ok || tm.Name != "Green" {
		t.Fatalf("bot on %v, want Green", tm)
	}
}

func TestBotJoinMovesTeamsMidRound(t *testing.T) {
	g, inst, ctx := harness(t) // no lobby: the round runs
	bot := join(g, inst, ctx, "BOT_Lina", 7)
	if err := g.BotJoin(ctx, bot, "yellow"); err != nil {
		t.Fatal(err)
	}
	tm, ok := g.teamOf(bot)
	if !ok || tm.Name != "Yellow" {
		t.Fatalf("bot on %v, want Yellow", tm)
	}
	if sp := g.arena.Spawns[tm.ID].Position; bot.Pose().X != float64(sp.X)+0.5 {
		t.Errorf("bot not moved to the Yellow spawn: %+v", bot.Pose())
	}
	if bot.slot(36).Item != "minecraft:wooden_sword" {
		t.Error("bot not re-kitted")
	}
}

func TestBotEnemyAndPlan(t *testing.T) {
	g, inst, ctx := harness(t)
	bot := join(g, inst, ctx, "BOT_Max", 7)
	if err := g.BotJoin(ctx, bot, "red"); err != nil {
		t.Fatal(err)
	}
	foe := join(g, inst, ctx, "Foe", 8)
	if err := g.BotJoin(ctx, foe, "blue"); err != nil {
		t.Fatal(err)
	}
	mate := join(g, inst, ctx, "Mate", 9)
	if err := g.BotJoin(ctx, mate, "red"); err != nil {
		t.Fatal(err)
	}
	if !g.BotEnemy(ctx, bot, foe) || g.BotEnemy(ctx, bot, mate) {
		t.Fatal("enemy test wrong: foe must be an enemy, a teammate not")
	}
	foe.SetGamemode(player.Spectator)
	if g.BotEnemy(ctx, bot, foe) {
		t.Fatal("a spectator is no enemy")
	}

	plan := g.BotPlan(ctx, bot)
	if plan.Idle || !plan.HasStation || plan.BlockItem != "minecraft:red_wool" ||
		plan.Currency != "minecraft:iron_ingot" || plan.Price != 4 || plan.Pack != 16 {
		t.Fatalf("plan: %+v", plan)
	}
	red, _ := g.teamByName("red")
	for _, tgt := range plan.Targets {
		for _, own := range g.arena.BedBlocks[red.team.ID] {
			if tgt == own {
				t.Fatal("the bot's own bed is a target")
			}
		}
	}
	if len(plan.Targets) != len(g.teams)-1 {
		t.Fatalf("%d targets, want one per enemy bed (%d)", len(plan.Targets), len(g.teams)-1)
	}
}

func TestBotPlanIdleBeforeTheRound(t *testing.T) {
	g, inst, ctx := lobbyHarness(t, 8)
	bot := join(g, inst, ctx, "BOT_Zoe", 7)
	if plan := g.BotPlan(ctx, bot); !plan.Idle {
		t.Fatalf("waiting bot should idle: %+v", plan)
	}
}

func TestBotBuysWool(t *testing.T) {
	g, inst, ctx := harness(t)
	bot := join(g, inst, ctx, "BOT_Kai", 7)
	if err := g.BotJoin(ctx, bot, "red"); err != nil {
		t.Fatal(err)
	}
	if g.BotBuy(ctx, bot, botBlocksKey) {
		t.Fatal("bought wool without iron")
	}
	bot.GiveStack(game.ItemStack{Item: "minecraft:iron_ingot", Count: 4})
	if !g.BotBuy(ctx, bot, botBlocksKey) || bot.CountItem("minecraft:red_wool") != 16 {
		t.Fatalf("buying wool: red wool %d", bot.CountItem("minecraft:red_wool"))
	}
}

// TestBotWaresAndGear: the plan lists gear with what the bot owns marked,
// the team and its bed; BotBuy pays for a sword, armour and a tool tier.
func TestBotWaresAndGear(t *testing.T) {
	g, inst, ctx := harness(t)
	bot := join(g, inst, ctx, "BOT_Ivy", 7)
	if err := g.BotJoin(ctx, bot, "red"); err != nil {
		t.Fatal(err)
	}
	ware := func(plan game.BotPlan, key string) (game.BotWare, bool) {
		for _, w := range plan.Wares {
			if w.Key == key {
				return w, true
			}
		}
		return game.BotWare{}, false
	}
	plan := g.BotPlan(ctx, bot)
	if plan.Team != "Red" || len(plan.Defend) != 2 {
		t.Fatalf("team %q, defend %v", plan.Team, plan.Defend)
	}
	for _, key := range []string{"stone_sword", "chain_armor", "pickaxe", "shears", "bow", "tnt"} {
		if w, ok := ware(plan, key); !ok || w.Owned || w.Price == 0 {
			t.Fatalf("ware %s: %+v (listed %v)", key, w, ok)
		}
	}
	if g.BotBuy(ctx, bot, "stone_sword") {
		t.Fatal("bought a sword without iron")
	}
	bot.GiveStack(game.ItemStack{Item: "minecraft:iron_ingot", Count: 64})
	for _, key := range []string{"stone_sword", "chain_armor", "pickaxe"} {
		if !g.BotBuy(ctx, bot, key) {
			t.Fatalf("buying %s failed", key)
		}
	}
	plan = g.BotPlan(ctx, bot)
	for _, key := range []string{"stone_sword", "chain_armor"} {
		if w, _ := ware(plan, key); !w.Owned {
			t.Fatalf("%s not marked owned after buying", key)
		}
	}
	if w, ok := ware(plan, "pickaxe"); !ok || w.Tier != 2 {
		t.Fatalf("next pickaxe tier: %+v", w)
	}
	if g.BotBuy(ctx, bot, "chain_armor") {
		t.Fatal("bought the same armour twice")
	}
}
