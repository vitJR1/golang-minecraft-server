package server

import (
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/schem"
	"minecraft-server/templates"
)

// TestBotMatchWatcher: /bot match makes an arena full of bots and puts the
// caller in it as a watcher — spectator mode, outside the game (no team, not
// an enemy, not counted) — and /leave takes them back out.
func TestBotMatchWatcher(t *testing.T) {
	s := New()
	s.TemplateDir = "../" + templates.Root
	sch, err := schem.LoadFile(templates.SchemFile(s.TemplateDir, templates.BedwarsDotaMap))
	if err != nil {
		t.Skip(err)
	}
	s.RegisterTemplate(templates.BedwarsDotaMap, sch.ToTemplateAt(-int(sch.Width)/2, 64, -int(sch.Length)/2))
	op := offlineHub(s, "Watcher")
	stop := make(chan struct{})
	defer close(stop)
	go func() { // the watcher's client reads everything (a whole map of chunks)
		for {
			select {
			case <-op.outbound:
			case <-stop:
				return
			}
		}
	}()

	name, n, err := s.botMatch(op, "bedwars-1x1", templates.BedwarsDotaMap, DifficultyEasy)
	if err != nil {
		t.Fatal(err)
	}
	inst := s.GetInstance(name)
	inst.Stop() // the test drives the ticks
	if n != 2 {
		t.Fatalf("%d bots in a duel, want 2", n)
	}
	if op.instance != inst || op.gamemode() != player.Spectator || !op.watching(inst) {
		t.Fatalf("watcher in %v, mode %v", op.instance.ID, op.gamemode())
	}
	for i := 0; i < 40; i++ {
		inst.runTick()
	}
	bots := s.botsIn(inst)
	ctx := inst.pluginCtx()
	if len(bots) != 2 || !inst.botGuide.BotEnemy(ctx, playerBridge{conn: bots[0]}, playerBridge{conn: bots[1]}) {
		t.Fatal("the bots aren't playing each other")
	}
	if plan := inst.botGuide.BotPlan(ctx, playerBridge{conn: bots[0]}); plan.Idle {
		t.Fatal("the round didn't start")
	}
	if inst.botGuide.BotEnemy(ctx, playerBridge{conn: bots[0]}, playerBridge{conn: op}) {
		t.Fatal("the watcher is in the game")
	}
	if bt, ok := inst.botGuide.(game.BotTeams); !ok || len(bt.BotTeams(ctx)) != 2 {
		t.Fatal("duel teams")
	}

	cmdLeave(op, nil)
	if op.instance == inst || op.watching(inst) || op.gamemode() == player.Spectator {
		t.Fatalf("after /leave: in %s, mode %v", op.instance.ID, op.gamemode())
	}
}
