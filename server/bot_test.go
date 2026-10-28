package server

import (
	"strings"
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// bot_test.go: bots end to end on a test world, driven tick by tick (the
// instance's own tick loop isn't running, so the test owns every tick).

// fakeGuide is a game.BotGuide for tests: enemies are players named
// "Enemy…", the plan is fixed, buying hands out wool.
type fakeGuide struct {
	plan  game.BotPlan
	buys  int
	joins []string
}

func (f *fakeGuide) BotJoin(_ *game.Ctx, _ game.PlayerHandle, team string) error {
	f.joins = append(f.joins, team)
	return nil
}

func (f *fakeGuide) BotEnemy(_ *game.Ctx, _, other game.PlayerHandle) bool {
	return strings.HasPrefix(other.Name(), "Enemy")
}

func (f *fakeGuide) BotPlan(*game.Ctx, game.PlayerHandle) game.BotPlan { return f.plan }

func (f *fakeGuide) BotBuy(_ *game.Ctx, p game.PlayerHandle, key string) bool {
	if p.CountItem(f.plan.Currency) < f.plan.Price {
		return false
	}
	p.TakeItem(f.plan.Currency, f.plan.Price)
	p.GiveStack(game.ItemStack{Item: f.plan.BlockItem, Count: f.plan.Pack})
	f.buys++
	return true
}

// botWorld: island A around the origin, island B 8 blocks of void away along
// +X, both floors at y=63; a plank block to destroy on island B.
func botWorld() (*Instance, world.Position) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	for x := -3; x <= 3; x++ {
		for z := -3; z <= 3; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
			inst.World.SetBlock(world.Position{X: x + 15, Y: 63, Z: z}, world.Stone)
		}
	}
	planks, _ := world.BlockByName("minecraft:oak_planks")
	target := world.Position{X: 16, Y: 64, Z: 0}
	inst.World.SetBlock(target, planks)
	inst.Combat = DefaultCombatConfig()
	inst.SetPvP(true)
	return inst, target
}

// addTestBot adds a bot through the server and parks it at (x, 64, z).
func addTestBot(t *testing.T, inst *Instance, guide *fakeGuide, diff Difficulty, x, z float64) *ClientConnection {
	t.Helper()
	inst.botGuide = guide
	b, err := inst.Server.AddBot(inst, "red", diff)
	if err != nil {
		t.Fatal(err)
	}
	b.player.MoveTo(x, 64, z, true)
	return b
}

// runTicks advances the instance's bots n ticks.
func runTicks(inst *Instance, n int) {
	for i := 0; i < n; i++ {
		tick := inst.tickCount.Add(1)
		inst.botTick(tick)
	}
}

func woolStack(t *testing.T, n int) itemStack {
	id, ok := world.ItemByName("minecraft:white_wool")
	if !ok {
		t.Fatal("white_wool not registered")
	}
	return itemStack{ID: id, Count: byte(n)}
}

func TestBotJoinsAsAPlayer(t *testing.T) {
	inst, _ := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{Idle: true}}
	b := addTestBot(t, inst, guide, DifficultyNormal, 0.5, 0.5)
	if !strings.HasPrefix(b.playerName, botPrefix) || len(b.playerName) > 16 {
		t.Fatalf("bot name %q", b.playerName)
	}
	if _, ok := inst.Players.Get(b.player.EntityID); !ok {
		t.Fatal("bot isn't in the instance's player list")
	}
	if len(guide.joins) != 1 || guide.joins[0] != "red" {
		t.Fatalf("guide joins: %v", guide.joins)
	}
	// Idle, it just stands on the floor.
	runTicks(inst, 40)
	if s := b.player.Snapshot(); s.Y != 64 || s.X != 0.5 {
		t.Fatalf("idle bot moved to %v,%v", s.X, s.Y)
	}
}

func TestBotNeedsAGuide(t *testing.T) {
	inst, _ := botWorld()
	if _, err := inst.Server.AddBot(inst, "red", DifficultyNormal); err == nil {
		t.Fatal("a bot joined an instance without a game guide")
	}
}

func TestBotBridgesAndBreaksTarget(t *testing.T) {
	inst, target := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{
		BlockItem: "minecraft:white_wool",
		Targets:   []world.Position{target},
	}}
	b := addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 32))

	runTicks(inst, 1200)
	if got := inst.World.GetBlock(target); got != world.Air {
		t.Fatalf("target still standing (%s); bot at %+v", got.Name, b.player.Snapshot())
	}
	bridge := 0
	for x := 4; x <= 11; x++ {
		if strings.HasSuffix(inst.World.GetBlock(world.Position{X: x, Y: 63, Z: 0}).Name, "_wool") {
			bridge++
		}
	}
	if bridge == 0 {
		t.Fatal("no wool bridge across the gap")
	}
	if s := b.player.Snapshot(); s.Y < 63 {
		t.Fatalf("bot fell into the void: %+v", s)
	}
}

func TestBotFightsNearbyEnemy(t *testing.T) {
	inst, _ := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{}}
	addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
	enemy := offlineConn(inst, "Enemy", player.Survival, 2.5, 64, 0.5)

	runTicks(inst, 200)
	drain(enemy)
	if enemy.player.Health() >= player.MaxHealth {
		t.Fatal("the bot never hit an enemy standing 2 blocks away")
	}
}

func TestBotIgnoresFriends(t *testing.T) {
	inst, _ := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{}}
	addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
	friend := offlineConn(inst, "Friend", player.Survival, 2.5, 64, 0.5)
	runTicks(inst, 200)
	drain(friend)
	if friend.player.Health() < player.MaxHealth {
		t.Fatal("the bot attacked a non-enemy")
	}
}

func TestBotBuysBlocksAtTheShop(t *testing.T) {
	inst, _ := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{
		BlockItem: "minecraft:white_wool", BuyKey: "wool",
		Currency: "minecraft:iron_ingot", Price: 4, Pack: 16,
		Shop: world.Position{X: 3, Y: 64, Z: 3}, HasShop: true,
		Station: world.Position{X: -2, Y: 64, Z: -2}, HasStation: true,
	}}
	b := addTestBot(t, inst, guide, DifficultyNormal, -2.5, -2.5)
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	b.inv.set(hotbarStart+2, itemStack{ID: iron, Count: 8})

	runTicks(inst, 400)
	if guide.buys != 2 || b.countItemName("minecraft:white_wool") != 32 {
		t.Fatalf("buys=%d wool=%d, want 2 purchases → 32 wool", guide.buys, b.countItemName("minecraft:white_wool"))
	}
	// Broke and short of blocks? It goes to the station.
	b.inv.set(hotbarStart+1, itemStack{})
	for s := int16(0); s < playerInvSize; s++ {
		if st := b.inv.get(s); st.ID == woolStack(t, 1).ID {
			b.inv.set(s, itemStack{})
		}
	}
	runTicks(inst, 300)
	if cell := b.feetCellNow(); !atStation(guide.plan.Station)(cell) {
		t.Fatalf("bot at %v, want the station %v", cell, guide.plan.Station)
	}
}

func TestBotLeavesWithItsGame(t *testing.T) {
	inst, _ := botWorld()
	b := addTestBot(t, inst, &fakeGuide{plan: game.BotPlan{Idle: true}}, DifficultyEasy, 0.5, 0.5)
	if err := inst.Server.MovePlayer(b, inst.Server.Hub, 0, 70, 0); err != nil {
		t.Fatal(err)
	}
	if !b.isClosed() {
		t.Fatal("a bot moved out of its game should leave the server")
	}
	if _, ok := inst.Players.Get(b.player.EntityID); ok {
		t.Fatal("removed bot still in the player list")
	}
}

func TestBotFightsWithItsBestWeapon(t *testing.T) {
	inst, _ := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{}}
	b := addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
	b.inv.set(hotbarStart, woolStack(t, 32)) // in hand: wool from bridging
	sword, _ := world.ItemByName("minecraft:iron_sword")
	b.inv.set(mainInvStart+5, itemStack{ID: sword, Count: 1}) // in the backpack
	b.heldSlot.Store(0)
	enemy := offlineConn(inst, "Enemy", player.Survival, 2.5, 64, 0.5)

	runTicks(inst, 60)
	drain(enemy)
	if b.heldItemName() != "minecraft:iron_sword" {
		t.Fatalf("fighting with %q, want the iron sword", b.heldItemName())
	}
	if enemy.player.Health() >= player.MaxHealth {
		t.Fatal("no hit landed")
	}
}
