package server

import (
	"os"
	"testing"
	"time"

	_ "minecraft-server/games/bedwars"
	"minecraft-server/schem"
	"minecraft-server/templates"
)

// TestBotOnDotaMap plays a bot alone on the shipped BedWars DOTA map and
// expects it to collect, buy wool, bridge to an enemy island and break a
// bed. Slow (seconds of route searches), so it runs only with BOTSIM=1.
// BOTTRACE=1 adds a log line every 100 ticks.
func TestBotOnDotaMap(t *testing.T) {
	if os.Getenv("BOTSIM") == "" {
		t.Skip("set BOTSIM=1 to play a bot on the DOTA map")
	}
	s := New()
	s.TemplateDir = "../" + templates.Root
	sch, err := schem.LoadFile(templates.SchemFile(s.TemplateDir, templates.BedwarsDotaMap))
	if err != nil {
		t.Skip(err)
	}
	s.RegisterTemplate(templates.BedwarsDotaMap, sch.ToTemplateAt(-int(sch.Width)/2, 64, -int(sch.Length)/2))
	name, err := s.CreateArena("bedwars", templates.BedwarsDotaMap, "botsim")
	if err != nil {
		t.Fatal(err)
	}
	inst := s.GetInstance(name)
	inst.Stop() // the test drives the ticks
	b, err := s.AddBot(inst, "red", DifficultyHard)
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.OnForceStart(); err != nil {
		t.Fatal(err)
	}
	start := -1
	for tick := 0; tick < 6000; tick++ {
		inst.runTick()
		// A route search runs off the tick; on a live server it gets 50 ms
		// a tick. Here, wait for it, so the result doesn't depend on how fast
		// the machine (or the race detector) is.
		for deadline := time.Now().Add(10 * time.Second); ; {
			far := b.bot.brain.far
			if far == nil || len(far) > 0 || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		// BOTTRACE=1 logs what the bot is up to every 5 s.
		if os.Getenv("BOTTRACE") != "" && tick%100 == 0 {
			br := &b.bot.brain
			p := b.bot.phys.Pos
			t.Logf("t=%d pos=(%.1f,%.1f,%.1f) key=%q idx=%d/%d shop=%v save=%v hard=%v dig=%v wool=%d iron=%d gold=%d dia=%d em=%d tnt=%d role=%v ware=%v flee=%v",
				tick, p.X, p.Y, p.Z, br.routeKey, br.routeIdx, len(br.route), br.shopping, inst.Tick() < br.farmUntil, br.hardCover, br.dig,
				b.countItemName(br.plan.BlockItem), b.countItemName("minecraft:iron_ingot"), b.countItemName("minecraft:gold_ingot"), b.countItemName("minecraft:diamond"), b.countItemName("minecraft:emerald"), b.countItemName("minecraft:tnt"), br.role,
				b.bot.wantWare(b.countItemName(br.plan.BlockItem)), inst.Tick() < br.fleeUntil)
		}
		targets := len(b.bot.brain.plan.Targets)
		if start < 0 && targets > 0 {
			start = targets
		}
		if start > 0 && targets < start {
			t.Logf("enemy bed broken after %d ticks", tick)
			return
		}
	}
	s2 := b.player.Snapshot()
	t.Fatalf("no enemy bed broken in 6000 ticks; bot at (%.1f, %.1f, %.1f)", s2.X, s2.Y, s2.Z)
}
