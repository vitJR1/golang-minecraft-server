package server

import (
	"math"
	"strings"
	"testing"
	"time"

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
	wares []string // wares bought, in order
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
	for i, w := range f.plan.Wares {
		if w.Key != key {
			continue
		}
		if w.Owned || p.CountItem(w.Currency) < w.Price {
			return false
		}
		p.TakeItem(w.Currency, w.Price)
		p.GiveStack(game.ItemStack{Item: "minecraft:" + key, Count: 1})
		if w.Kind != game.WareTNT && w.Kind != game.WareArrows { // consumables stay on offer
			f.plan.Wares[i].Owned = true
		}
		f.wares = append(f.wares, key)
		return true
	}
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

// runTicks advances the instance's bots (and projectiles, TNT) n ticks,
// draining the given players' queues as it goes. A route search a bot runs
// off the tick is waited for (a live server gives it 50 ms a tick; this loop
// runs ticks back to back), so results don't depend on the machine's load.
func runTicks(inst *Instance, n int, players ...*ClientConnection) {
	for i := 0; i < n; i++ {
		tick := inst.tickCount.Add(1)
		inst.Players.Range(func(c *ClientConnection) {
			if !c.isBot() {
				return
			}
			for deadline := time.Now().Add(10 * time.Second); ; {
				far := c.bot.brain.far
				if far == nil || len(far) > 0 || time.Now().After(deadline) {
					break
				}
				time.Sleep(100 * time.Microsecond)
			}
		})
		inst.botTick(tick)
		inst.projectileTick(tick)
		inst.tntTick(tick)
		inst.itemTick(tick)
		for _, p := range players {
			drain(p)
		}
	}
}

func itemOf(t *testing.T, item string, n int) itemStack {
	t.Helper()
	id, ok := world.ItemByName(item)
	if !ok {
		t.Fatalf("%s not registered", item)
	}
	return itemStack{ID: id, Count: byte(n)}
}

// gapWorld: two strips along X at y=63, z=-1..1 — x=-3..2 and x=3+gap..12+gap.
func gapWorld(gap int) *Instance {
	inst := bareInstance(New(), world.NewMemoryWorld())
	for z := -1; z <= 1; z++ {
		for x := -3; x <= 12+gap; x++ {
			if x < 3 || x >= 3+gap {
				inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
			}
		}
	}
	inst.Combat = DefaultCombatConfig()
	inst.SetPvP(true)
	return inst
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

// TestBotLeapsToAnEnemy: no blocks, a two-block gap to the enemy — a hard
// bot sprint-jumps it.
func TestBotLeapsToAnEnemy(t *testing.T) {
	inst := gapWorld(2)
	b := addTestBot(t, inst, &fakeGuide{plan: game.BotPlan{}}, DifficultyHard, 0.5, 0.5)
	enemy := offlineConn(inst, "Enemy", player.Survival, 9.5, 64, 0.5)
	runTicks(inst, 300, enemy)
	if s := b.player.Snapshot(); s.Y < 63 {
		t.Fatalf("bot fell: %+v", s)
	}
	if enemy.player.Health() >= player.MaxHealth {
		t.Fatalf("never reached the enemy; bot at %+v", b.player.Snapshot())
	}
}

// TestBotBridgesToAnEnemy: a gap too wide to leap — it bridges.
func TestBotBridgesToAnEnemy(t *testing.T) {
	inst := gapWorld(6)
	b := addTestBot(t, inst, &fakeGuide{plan: game.BotPlan{BlockItem: "minecraft:white_wool"}}, DifficultyHard, 0.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 32))
	enemy := offlineConn(inst, "Enemy", player.Survival, 11.5, 64, 0.5)
	runTicks(inst, 600, enemy)
	if enemy.player.Health() >= player.MaxHealth {
		t.Fatalf("never reached the enemy; bot at %+v", b.player.Snapshot())
	}
}

// TestBotBuildsStairsToAnEnemy: the enemy waits on a 3-high pillar; the bot
// builds up to it.
func TestBotBuildsStairsToAnEnemy(t *testing.T) {
	inst := gapWorld(0)
	for y := 64; y <= 66; y++ {
		inst.World.SetBlock(world.Position{X: 6, Y: y, Z: 0}, world.Stone)
	}
	b := addTestBot(t, inst, &fakeGuide{plan: game.BotPlan{BlockItem: "minecraft:white_wool"}}, DifficultyNormal, 0.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 32))
	enemy := offlineConn(inst, "Enemy", player.Survival, 6.5, 67, 0.5)
	runTicks(inst, 600, enemy)
	if enemy.player.Health() >= player.MaxHealth {
		t.Fatalf("never reached the enemy on the pillar; bot at %+v", b.player.Snapshot())
	}
}

// TestBotShootsAnUnreachableEnemy: across the void, no blocks, a bow.
func TestBotShootsAnUnreachableEnemy(t *testing.T) {
	inst, _ := botWorld()
	b := addTestBot(t, inst, &fakeGuide{plan: game.BotPlan{}}, DifficultyHard, 0.5, 0.5)
	b.inv.set(hotbarStart+3, itemOf(t, "minecraft:bow", 1))
	b.inv.set(mainInvStart, itemOf(t, "minecraft:arrow", 16))
	enemy := offlineConn(inst, "Enemy", player.Survival, 13.5, 64, 0.5)
	runTicks(inst, 400, enemy)
	if b.countItemName("minecraft:arrow") == 16 {
		t.Fatal("no arrow shot")
	}
	if enemy.player.Health() >= player.MaxHealth {
		t.Fatalf("no arrow hit (%d left)", b.countItemName("minecraft:arrow"))
	}
}

// TestBotLeavesAnUnreachableEnemy: no bow, no way over — it gets on with
// its plan instead of staring.
func TestBotLeavesAnUnreachableEnemy(t *testing.T) {
	inst, _ := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{Station: world.Position{X: -2, Y: 64, Z: -2}, HasStation: true}}
	b := addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
	enemy := offlineConn(inst, "Enemy", player.Survival, 13.5, 64, 0.5)
	runTicks(inst, 200, enemy)
	if cell := b.feetCellNow(); !atStation(guide.plan.Station)(cell) {
		t.Fatalf("bot at %v, want it gone on to the station", cell)
	}
}

// TestBotBuysGear: with iron to spare it buys a sword, keeping a pack of
// wool's worth; armour it can't afford it leaves.
func TestBotBuysGear(t *testing.T) {
	inst, _ := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{
		BlockItem: "minecraft:white_wool", BuyKey: "wool",
		Currency: "minecraft:iron_ingot", Price: 4, Pack: 16,
		Shop: world.Position{X: 3, Y: 64, Z: 3}, HasShop: true,
		Wares: []game.BotWare{
			{Key: "stone_sword", Kind: game.WareSword, Tier: 1, Currency: "minecraft:iron_ingot", Price: 10},
			{Key: "iron_chestplate", Kind: game.WareArmor, Tier: 2, Currency: "minecraft:gold_ingot", Price: 12},
		},
	}}
	b := addTestBot(t, inst, guide, DifficultyNormal, -2.5, -2.5)
	b.inv.set(hotbarStart+1, woolStack(t, 64))
	b.inv.set(hotbarStart+2, itemOf(t, "minecraft:iron_ingot", 12))
	runTicks(inst, 300)
	if len(guide.wares) != 1 || guide.wares[0] != "stone_sword" {
		t.Fatalf("bought %v, want the stone sword", guide.wares)
	}
	if b.countItemName("minecraft:stone_sword") != 1 {
		t.Fatal("no sword in the inventory")
	}
}

// TestBotBlastsHardCover: a bed under end stone (slow by hand, but TNT gets
// through it) and the bot carries TNT — it places a lit TNT against the
// cover and runs.
func TestBotBlastsHardCover(t *testing.T) {
	inst, target := botWorld()
	inst.SetTNTAutoPrime(true)
	endStone, _ := world.BlockByName("minecraft:end_stone")
	for _, d := range [][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}, {0, 1, 0}} {
		inst.World.SetBlock(target.Add(d[0], d[1], d[2]), endStone)
	}
	guide := &fakeGuide{plan: game.BotPlan{
		BlockItem: "minecraft:white_wool",
		Home:      world.Position{X: 0, Y: 64, Z: 0},
		Targets:   []world.Position{target},
	}}
	b := addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 32))
	b.inv.set(hotbarStart+4, itemOf(t, "minecraft:tnt", 1))
	lit := false
	for i := 0; i < 1500 && !lit; i++ {
		runTicks(inst, 1)
		inst.tntMu.Lock()
		lit = len(inst.tnts) > 0
		inst.tntMu.Unlock()
	}
	if !lit {
		t.Fatalf("no TNT lit; bot at %+v", b.player.Snapshot())
	}
	runTicks(inst, 45) // fuse 50: it should be clear by now
	s := b.player.Snapshot()
	if d := math.Hypot(s.X-float64(target.X), s.Z-float64(target.Z)); d < 5 {
		t.Fatalf("bot still %.1f blocks from its TNT", d)
	}
}

// TestBotTeamRoles: of three bots on one team, the first is the pusher, the
// second covers the team's bed with wool, the third is the archer.
func TestBotTeamRoles(t *testing.T) {
	inst, target := botWorld()
	bed, _ := world.BlockByName("minecraft:red_bed")
	own := []world.Position{{X: -2, Y: 64, Z: 0}, {X: -1, Y: 64, Z: 0}}
	for _, p := range own {
		inst.World.SetBlock(p, bed)
	}
	guide := &fakeGuide{plan: game.BotPlan{
		Team: "red", Defend: own,
		BlockItem: "minecraft:white_wool",
		Targets:   []world.Position{target},
	}}
	var bots []*ClientConnection
	for i := 0; i < 3; i++ {
		b := addTestBot(t, inst, guide, DifficultyHard, 1.5, float64(i)-0.5)
		b.inv.set(hotbarStart+1, woolStack(t, 32))
		bots = append(bots, b)
	}
	runTicks(inst, 20) // roles are handed out before anyone has gone anywhere
	roles := []botRole{bots[0].bot.brain.role, bots[1].bot.brain.role, bots[2].bot.brain.role}
	if roles[0] != rolePusher || roles[1] != roleDefender || roles[2] != roleArcher {
		t.Fatalf("roles %v, want pusher, defender, archer", roles)
	}
	runTicks(inst, 1200)
	if left := bedCover(inst.World.GetBlock, own); len(left) != 0 {
		t.Fatalf("bed cover still open at %v", left)
	}
	if got := inst.World.GetBlock(target); got != world.Air {
		t.Fatalf("the pusher didn't break the enemy bed (%s)", got.Name)
	}
}

// TestBotTeamBrain: one brain per team — an enemy at the bed pulls a
// second defender in (and the defenders go for it), and every attacker goes
// for the same bed.
func TestBotTeamBrain(t *testing.T) {
	inst, target := botWorld()
	other := world.Position{X: 16, Y: 64, Z: 3}
	planks, _ := world.BlockByName("minecraft:oak_planks")
	inst.World.SetBlock(other, planks)
	own := []world.Position{{X: -2, Y: 64, Z: 0}, {X: -1, Y: 64, Z: 0}}
	guide := &fakeGuide{plan: game.BotPlan{
		Team: "red", Defend: own,
		Targets: []world.Position{target, other},
	}}
	var bots []*ClientConnection
	for i := 0; i < 4; i++ {
		bots = append(bots, addTestBot(t, inst, guide, DifficultyNormal, float64(i)-1.5, 2.5))
	}
	runTicks(inst, 20)
	roles := func() []botRole {
		var out []botRole
		for _, b := range bots {
			out = append(out, b.bot.brain.role)
		}
		return out
	}
	if r := roles(); r[0] != rolePusher || r[1] != roleDefender || r[2] != roleArcher || r[3] != roleAttacker {
		t.Fatalf("calm roles %v", r)
	}
	// Bots 1 and 4 attack: both put the team's focus first.
	if a, b := bots[0].bot.teamTargets(), bots[3].bot.teamTargets(); a[0] != b[0] {
		t.Fatalf("attackers split: %v vs %v", a[0], b[0])
	}

	enemy := offlineConn(inst, "Enemy", player.Survival, -1.5, 64, -2.5)
	runTicks(inst, 20, enemy)
	if r := roles(); r[1] != roleDefender || r[2] != roleDefender {
		t.Fatalf("roles with an enemy at the bed %v, want two defenders", r)
	}
	if bots[2].bot.brain.target != enemy {
		t.Fatal("the second defender isn't going for the intruder")
	}
}

// teamsGuide is a fakeGuide that names its teams (game.BotTeams).
type teamsGuide struct{ fakeGuide }

func (*teamsGuide) BotTeams(*game.Ctx) []string { return []string{"red", "blue"} }

func TestBotSuggestions(t *testing.T) {
	inst, _ := botWorld()
	op := offlineConn(inst, "Op", player.Creative, 0.5, 64, 0.5)
	guide := &teamsGuide{fakeGuide{plan: game.BotPlan{Idle: true}}}
	inst.botGuide = guide
	inst.ID = "bots"
	inst.Server.mu.Lock()
	inst.Server.instances[inst.ID] = inst // so /bot remove lists its bots
	inst.Server.mu.Unlock()
	b, err := inst.Server.AddBot(inst, "red", DifficultyEasy)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"/bot ":         {"add", "clear", "list", "match", "remove"},
		"/bot a":        {"add"},
		"/bot add ":     {"blue", "red"},
		"/bot add r":    {"red"},
		"/bot add red ": {"easy", "hard", "normal"},
		"/bot remove ":  {b.playerName},
		"/bot list ":    nil,
	}
	for text, want := range cases {
		_, _, got := inst.Server.Suggestions(op, text)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%q → %v, want %v", text, got, want)
		}
	}
}

// TestBotRaidsEmeraldsAndShops: emeralds piled at a generator down the
// strip, and the bot wants a diamond sword (4 emeralds) — it goes for the
// pile, picks it up and goes back to the shop to buy the sword.
func TestBotRaidsEmeraldsAndShops(t *testing.T) {
	inst := gapWorld(0)
	gen := world.Position{X: 10, Y: 64, Z: 0}
	guide := &fakeGuide{plan: game.BotPlan{
		BlockItem: "minecraft:white_wool", BuyKey: "wool",
		Currency: "minecraft:iron_ingot", Price: 4, Pack: 16,
		Home: world.Position{X: 0, Y: 64, Z: 0},
		Shop: world.Position{X: -2, Y: 64, Z: 1}, HasShop: true,
		Wares: []game.BotWare{
			{Key: "diamond_sword", Kind: game.WareSword, Tier: 3, Currency: "minecraft:emerald", Price: 4},
		},
		Pickups: []game.BotPickup{{Pos: gen, Item: "minecraft:emerald"}},
	}}
	b := addTestBot(t, inst, guide, DifficultyNormal, 0.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 64)) // no reason to shop for blocks
	inst.DropItem(10.5, 64, 0.5, "minecraft:emerald", 4)

	got := false
	for i := 0; i < 800 && !got; i++ {
		runTicks(inst, 1)
		got = b.countItemName("minecraft:emerald") == 4
	}
	if !got {
		t.Fatalf("never picked the emeralds up; bot at %+v", b.player.Snapshot())
	}
	runTicks(inst, 800)
	if len(guide.wares) != 1 || guide.wares[0] != "diamond_sword" {
		t.Fatalf("bought %v, want the diamond sword (bot at %+v, %d emeralds)", guide.wares, b.player.Snapshot(), b.countItemName("minecraft:emerald"))
	}
}

// TestBotIgnoresUselessPiles: nothing it wants costs emeralds — it leaves
// the pile alone.
func TestBotIgnoresUselessPiles(t *testing.T) {
	inst := gapWorld(0)
	gen := world.Position{X: 12, Y: 64, Z: 0}
	guide := &fakeGuide{plan: game.BotPlan{
		Home:    world.Position{X: 0, Y: 64, Z: 0},
		Station: world.Position{X: -2, Y: 64, Z: 0}, HasStation: true,
		Pickups: []game.BotPickup{{Pos: gen, Item: "minecraft:emerald"}},
	}}
	b := addTestBot(t, inst, guide, DifficultyNormal, 0.5, 0.5)
	inst.DropItem(12.5, 64, 0.5, "minecraft:emerald", 4)
	runTicks(inst, 300)
	if b.countItemName("minecraft:emerald") != 0 {
		t.Fatal("raided a pile it has no use for")
	}
}

// TestBotFightsOnAtEnemyBase: a normal bot low on health falls back at
// home, but at an enemy bed it fights on.
func TestBotFightsOnAtEnemyBase(t *testing.T) {
	for _, tc := range []struct {
		name    string
		x       float64 // where bot and enemy meet
		retreat bool
	}{{"home", 0.5, true}, {"enemy base", 15.5, false}} {
		t.Run(tc.name, func(t *testing.T) {
			inst, target := botWorld()
			guide := &fakeGuide{plan: game.BotPlan{
				Home:    world.Position{X: 0, Y: 64, Z: -2},
				Targets: []world.Position{target},
			}}
			b := addTestBot(t, inst, guide, DifficultyNormal, tc.x, -1.5)
			b.player.SetHealth(4)
			enemy := offlineConn(inst, "Enemy", player.Survival, tc.x, 64, 0.5)
			runTicks(inst, 60, enemy)
			if retreated := b.bot.brain.retreatUntil > 0; retreated != tc.retreat {
				t.Fatalf("retreated=%v, want %v", retreated, tc.retreat)
			}
			if !tc.retreat && enemy.player.Health() >= player.MaxHealth {
				t.Fatal("didn't fight on")
			}
		})
	}
}

// TestBotBreaksTheBedBeforeShopping: at an enemy bed with emeralds for a
// diamond sword, the bot breaks the bed instead of running home to buy.
func TestBotBreaksTheBedBeforeShopping(t *testing.T) {
	inst, target := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{
		BlockItem: "minecraft:white_wool", BuyKey: "wool",
		Currency: "minecraft:iron_ingot", Price: 4, Pack: 16,
		Home: world.Position{X: 0, Y: 64, Z: 0},
		Shop: world.Position{X: -2, Y: 64, Z: 1}, HasShop: true,
		Wares: []game.BotWare{
			{Key: "diamond_sword", Kind: game.WareSword, Tier: 3, Currency: "minecraft:emerald", Price: 4},
		},
		Pickups: []game.BotPickup{{Pos: world.Position{X: 2, Y: 64, Z: 2}, Item: "minecraft:emerald"}},
		Targets: []world.Position{target},
	}}
	b := addTestBot(t, inst, guide, DifficultyHard, 14.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 32))
	b.inv.set(hotbarStart+2, itemOf(t, "minecraft:emerald", 8))
	runTicks(inst, 200)
	if got := inst.World.GetBlock(target); got != world.Air {
		t.Fatalf("bed still standing (%s); bot at %+v", got.Name, b.player.Snapshot())
	}
	if len(guide.wares) != 0 {
		t.Fatalf("went shopping first: %v", guide.wares)
	}
}

// TestArcherShootsFarEnemies: the team's archer engages an enemy it sees 60
// blocks off across the void — far beyond melee aggro — and hits.
func TestArcherShootsFarEnemies(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	for x := -3; x <= 3; x++ {
		for z := -3; z <= 3; z++ {
			inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
			inst.World.SetBlock(world.Position{X: x + 60, Y: 63, Z: z}, world.Stone)
		}
	}
	inst.Combat = DefaultCombatConfig()
	inst.SetPvP(true)
	guide := &fakeGuide{plan: game.BotPlan{Team: "red"}}
	var bots []*ClientConnection
	for i := 0; i < 3; i++ {
		bots = append(bots, addTestBot(t, inst, guide, DifficultyHard, float64(i)-1.5, -2.5))
	}
	runTicks(inst, 20) // the team brain hands out roles
	var archer *ClientConnection
	for _, b := range bots {
		if b.bot.brain.role == roleArcher {
			archer = b
		}
	}
	if archer == nil {
		t.Fatal("no archer among three bots")
	}
	archer.inv.set(hotbarStart+3, itemOf(t, "minecraft:bow", 1))
	archer.inv.set(mainInvStart, itemOf(t, "minecraft:arrow", 32))
	enemy := offlineConn(inst, "Enemy", player.Survival, 60.5, 64, 0.5)
	runTicks(inst, 600, enemy)
	if archer.countItemName("minecraft:arrow") == 32 {
		t.Fatal("the archer never shot")
	}
	if enemy.player.Health() >= player.MaxHealth {
		t.Fatalf("no arrow hit from 60 blocks (%d arrows left)", archer.countItemName("minecraft:arrow"))
	}
}

// TestBotHopsFaster: down a long straight floor a bunny-hopping bot gets
// there sooner than one that just sprints.
func TestBotHopsFaster(t *testing.T) {
	run := func(hop bool) int {
		inst := bareInstance(New(), world.NewMemoryWorld())
		for x := -3; x <= 50; x++ {
			for z := -1; z <= 1; z++ {
				inst.World.SetBlock(world.Position{X: x, Y: 63, Z: z}, world.Stone)
			}
		}
		station := world.Position{X: 45, Y: 64, Z: 0}
		guide := &fakeGuide{plan: game.BotPlan{Station: station, HasStation: true}}
		b := addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
		b.bot.skill.hop = hop
		for tick := 1; tick <= 600; tick++ {
			runTicks(inst, 1)
			if atStation(station)(b.feetCellNow()) && b.bot.phys.OnGround {
				return tick
			}
		}
		t.Fatalf("hop=%v: never got there; at %+v", hop, b.bot.phys.Pos)
		return 0
	}
	walk, hop := run(false), run(true)
	if float64(hop) > 0.92*float64(walk) {
		t.Fatalf("hopping took %d ticks, sprinting %d: not faster", hop, walk)
	}
	t.Logf("45 blocks: sprint %d ticks, hop %d ticks", walk, hop)
}

// TestDefenderFortifiesThenFights: the defender wraps the bed in two layers
// and then leaves the base for the fight instead of idling at the forge.
func TestDefenderFortifiesThenFights(t *testing.T) {
	inst, target := botWorld()
	bed, _ := world.BlockByName("minecraft:red_bed")
	own := []world.Position{{X: -2, Y: 64, Z: 0}, {X: -1, Y: 64, Z: 0}}
	for _, p := range own {
		inst.World.SetBlock(p, bed)
	}
	guide := &fakeGuide{plan: game.BotPlan{
		Team: "red", Defend: own,
		BlockItem: "minecraft:white_wool",
		Station:   world.Position{X: 2, Y: 64, Z: 2}, HasStation: true,
		Targets: []world.Position{target},
	}}
	var bots []*ClientConnection
	for i := 0; i < 2; i++ {
		b := addTestBot(t, inst, guide, DifficultyHard, 1.5, float64(i)+1.5)
		b.inv.set(hotbarStart+1, woolStack(t, 64))
		bots = append(bots, b)
	}
	runTicks(inst, 20)
	def := bots[1]
	if def.bot.brain.role != roleDefender {
		t.Fatalf("second bot is a %v", def.bot.brain.role)
	}
	left := false
	for i := 0; i < 1500 && !left; i++ {
		runTicks(inst, 1)
		left = def.bot.phys.Pos.X > 8 // out over the void toward the enemy
	}
	at := inst.World.GetBlock
	if n, m := len(bedCover(at, own)), len(bedOuterCover(at, own)); n != 0 || m != 0 {
		t.Fatalf("cover still open: %d inner, %d outer %v", n, m, bedOuterCover(at, own))
	}
	if !left {
		t.Fatalf("the defender stayed home; at %+v", def.bot.phys.Pos)
	}
}

// TestBotKeepsItsRouteDownSteps: walking down one-block steps, the bot's
// feet sit a cell above the next step while it's on the edge — that's not
// "knocked off the route" (it used to drop the route there and dither).
func TestBotKeepsItsRouteDownSteps(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	for x := -3; x <= 14; x++ {
		floor := 66 - max(0, min(3, (x-2)/3)) // 66, …, then a step down every 3 blocks to 63
		for z := -1; z <= 1; z++ {
			for y := 60; y <= floor; y++ {
				inst.World.SetBlock(world.Position{X: x, Y: y, Z: z}, world.Stone)
			}
		}
	}
	station := world.Position{X: 13, Y: 64, Z: 0}
	guide := &fakeGuide{plan: game.BotPlan{Station: station, HasStation: true}}
	b := addTestBot(t, inst, guide, DifficultyEasy, 0.5, 0.5) // easy: no hopping
	b.player.MoveTo(0.5, 67, 0.5, true)
	searches := 0
	var lastAt uint64
	for i := 0; i < 300; i++ {
		runTicks(inst, 1)
		if br := &b.bot.brain; br.route != nil && br.routeAt != lastAt {
			searches++
			lastAt = br.routeAt
		}
		if atStation(station)(b.feetCellNow()) {
			if searches > 1 {
				t.Fatalf("searched the route %d times on the way down", searches)
			}
			if len(b.bot.fallSpots()) != 0 {
				t.Fatalf("the route's own steps down counted as falls: %v", b.bot.fallSpots())
			}
			return
		}
	}
	t.Fatalf("never got down; at %+v", b.bot.phys.Pos)
}

// TestPusherTowersAndDropsTNT: the target is covered on top and at the
// sides; the team's pusher bridges over, towers up beside it, drops a TNT
// on top of the cover and then gets the target.
func TestPusherTowersAndDropsTNT(t *testing.T) {
	inst, target := botWorld()
	inst.SetTNTAutoPrime(true)
	wool, _ := world.BlockByName("minecraft:white_wool")
	for _, d := range [][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}, {0, 1, 0}} {
		inst.World.SetBlock(target.Add(d[0], d[1], d[2]), wool)
	}
	guide := &fakeGuide{plan: game.BotPlan{
		Team:      "red",
		BlockItem: "minecraft:white_wool",
		Home:      world.Position{X: 0, Y: 64, Z: 0},
		Targets:   []world.Position{target},
	}}
	b := addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 64))
	b.inv.set(hotbarStart+4, itemOf(t, "minecraft:tnt", 2))
	runTicks(inst, 20)
	if b.bot.brain.role != rolePusher {
		t.Fatalf("a lone bot with beds to break is a %v", b.bot.brain.role)
	}
	var dropped *primedTNT
	maxFeet := 0.0
	for i := 0; i < 1500 && dropped == nil; i++ {
		runTicks(inst, 1)
		if b.bot.phys.Pos.X > 12 {
			maxFeet = math.Max(maxFeet, b.bot.phys.Pos.Y)
		}
		inst.tntMu.Lock()
		if len(inst.tnts) > 0 {
			dropped = inst.tnts[0]
		}
		inst.tntMu.Unlock()
	}
	if dropped == nil {
		t.Fatalf("no TNT dropped; bot at %+v", b.bot.phys.Pos)
	}
	if dropped.y < float64(target.Y)+2 || math.Abs(dropped.x-(float64(target.X)+0.5)) > 0.6 {
		t.Fatalf("TNT at (%.1f, %.1f, %.1f), want on top of the cover over %v", dropped.x, dropped.y, dropped.z, target)
	}
	if maxFeet < float64(target.Y)+2 {
		t.Fatalf("didn't tower up (feet reached %.2f)", maxFeet)
	}
	runTicks(inst, 600)
	if got := inst.World.GetBlock(target); got != world.Air {
		t.Fatalf("target still standing (%s); bot at %+v", got.Name, b.bot.phys.Pos)
	}
}

// TestPusherBuysTNTFirst: with gold for two TNT, the pusher buys TNT before
// anything else.
func TestPusherBuysTNTFirst(t *testing.T) {
	inst, target := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{
		Team:      "red",
		BlockItem: "minecraft:white_wool", BuyKey: "wool",
		Currency: "minecraft:iron_ingot", Price: 4, Pack: 16,
		Home: world.Position{X: 0, Y: 64, Z: 0},
		Shop: world.Position{X: -2, Y: 64, Z: 1}, HasShop: true,
		Wares: []game.BotWare{
			{Key: "tnt", Kind: game.WareTNT, Currency: "minecraft:gold_ingot", Price: 4},
			{Key: "iron_sword", Kind: game.WareSword, Tier: 2, Currency: "minecraft:gold_ingot", Price: 7},
		},
		Targets: []world.Position{target},
	}}
	b := addTestBot(t, inst, guide, DifficultyNormal, 0.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 64))
	b.inv.set(hotbarStart+2, itemOf(t, "minecraft:gold_ingot", 8))
	runTicks(inst, 200)
	if len(guide.wares) == 0 || guide.wares[0] != "tnt" {
		t.Fatalf("bought %v, want TNT first", guide.wares)
	}
}

// TestBotWidensWhereItFell: knocked off a one-wide bridge twice at the same
// spot, the bot widens the bridge there the next time it passes.
func TestBotWidensWhereItFell(t *testing.T) {
	inst, target := botWorld()
	for x := 4; x <= 11; x++ { // a one-wide stone bridge between the islands
		inst.World.SetBlock(world.Position{X: x, Y: 63, Z: 0}, world.Stone)
	}
	guide := &fakeGuide{plan: game.BotPlan{
		Team:      "red",
		BlockItem: "minecraft:white_wool",
		Home:      world.Position{X: 0, Y: 64, Z: 0},
		Targets:   []world.Position{target},
	}}
	b := addTestBot(t, inst, guide, DifficultyEasy, 0.5, 0.5) // easy: no hops
	b.inv.set(hotbarStart+1, woolStack(t, 64))
	spot := world.Position{X: 7, Y: 64, Z: 0}
	for fall := 0; fall < 2; fall++ {
		pushed := false
		for i := 0; i < 600 && !pushed; i++ {
			runTicks(inst, 1)
			if b.feetCellNow() == spot && b.bot.phys.OnGround {
				b.bot.pushVelocity(0, 0.3, 0.6) // a hit from the side
				pushed = true
			}
		}
		if !pushed {
			t.Fatalf("fall %d: never reached the spot; at %+v", fall, b.bot.phys.Pos)
		}
		runTicks(inst, 40) // down it goes
		if b.bot.phys.Pos.Y > 60 {
			t.Fatalf("fall %d: didn't fall; at %+v", fall, b.bot.phys.Pos)
		}
		b.player.MoveTo(0.5, 64, 0.5, true) // "respawn" at home
	}
	// The fall is put on the last route cell it stood on: the spot or the
	// one before.
	n := 0
	for p, k := range b.bot.fallSpots() {
		if p.Y == spot.Y && p.Z == spot.Z && abs(p.X-spot.X) <= 1 {
			n += k
		}
	}
	if n < botFallsToReinforce {
		t.Fatalf("falls near %v: %d, want ≥ %d (spots %v)", spot, n, botFallsToReinforce, b.bot.fallSpots())
	}
	runTicks(inst, 600)
	wide := 0
	for x := 6; x <= 8; x++ {
		for _, z := range []int{-1, 1} {
			if strings.HasSuffix(inst.World.GetBlock(world.Position{X: x, Y: 63, Z: z}).Name, "_wool") {
				wide++
			}
		}
	}
	if wide < 4 {
		var placed []world.Position
		for x := 2; x <= 13; x++ {
			for z := -2; z <= 2; z++ {
				p := world.Position{X: x, Y: 63, Z: z}
				if strings.HasSuffix(inst.World.GetBlock(p).Name, "_wool") {
					placed = append(placed, p)
				}
			}
		}
		t.Fatalf("only %d side blocks around the fall spot, want the bridge widened; wool at %v; spots %v; bot at %+v", wide, placed, b.bot.fallSpots(), b.bot.phys.Pos)
	}
}

// TestBotDigsRoundObsidian: the target is walled in obsidian on every side
// but one (wool). With no pickaxe the bot never starts on the obsidian — it
// goes round to the wool side and digs through there.
func TestBotDigsRoundObsidian(t *testing.T) {
	inst, target := botWorld()
	obsidian, _ := world.BlockByName("minecraft:obsidian")
	wool, _ := world.BlockByName("minecraft:white_wool")
	for _, d := range [][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 1, 0}} {
		inst.World.SetBlock(target.Add(d[0], d[1], d[2]), obsidian)
	}
	soft := target.Add(0, 0, 1)
	inst.World.SetBlock(soft, wool)
	guide := &fakeGuide{plan: game.BotPlan{
		BlockItem: "minecraft:white_wool",
		Targets:   []world.Position{target},
	}}
	b := addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 64))
	for i := 0; i < 2000 && inst.World.GetBlock(target) != world.Air; i++ {
		runTicks(inst, 1)
		if d := b.bot.brain.dig; d != nil && inst.World.GetBlock(d.pos) == obsidian {
			t.Fatalf("started digging obsidian at %v by hand", d.pos)
		}
	}
	if got := inst.World.GetBlock(target); got != world.Air {
		t.Fatalf("target still standing (%s); bot at %+v", got.Name, b.bot.phys.Pos)
	}
	if inst.World.GetBlock(target.Add(-1, 0, 0)) != obsidian {
		t.Fatal("the obsidian wall was broken")
	}
}

// TestBotBuysAPickaxeForObsidian: obsidian on every side of the target, no
// pickaxe, no TNT — the bot leaves the enemy base for the shop, buys the
// pickaxe that gets through and comes back to break the target.
func TestBotBuysAPickaxeForObsidian(t *testing.T) {
	inst, target := botWorld()
	for x := 4; x <= 11; x++ { // a stone bridge between the islands
		inst.World.SetBlock(world.Position{X: x, Y: 63, Z: 0}, world.Stone)
	}
	obsidian, _ := world.BlockByName("minecraft:obsidian")
	for _, d := range [][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}, {0, 1, 0}} {
		inst.World.SetBlock(target.Add(d[0], d[1], d[2]), obsidian)
	}
	guide := &fakeGuide{plan: game.BotPlan{
		BlockItem: "minecraft:white_wool",
		Home:      world.Position{X: 0, Y: 64, Z: 0},
		Shop:      world.Position{X: -2, Y: 64, Z: 1}, HasShop: true,
		Wares: []game.BotWare{
			{Key: "diamond_pickaxe", Kind: game.WarePickaxe, Tier: 4, Currency: "minecraft:gold_ingot", Price: 6},
		},
		Targets: []world.Position{target},
	}}
	b := addTestBot(t, inst, guide, DifficultyHard, 14.5, 0.5) // at the enemy bed
	b.inv.set(hotbarStart+1, woolStack(t, 32))
	b.inv.set(hotbarStart+2, itemOf(t, "minecraft:gold_ingot", 10))
	for i := 0; i < 3000 && inst.World.GetBlock(target) != world.Air; i++ {
		runTicks(inst, 1)
	}
	if len(guide.wares) == 0 || guide.wares[0] != "diamond_pickaxe" {
		t.Fatalf("bought %v, want the diamond pickaxe", guide.wares)
	}
	if got := inst.World.GetBlock(target); got != world.Air {
		t.Fatalf("target still standing (%s); bot at %+v", got.Name, b.bot.phys.Pos)
	}
}

func TestTNTBreaks(t *testing.T) {
	for name, want := range map[string]bool{
		"minecraft:obsidian": false, "minecraft:end_stone": true,
		"minecraft:white_wool": true, "minecraft:oak_planks": true,
	} {
		b, ok := world.BlockByName(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if got := tntBreaks(b); got != want {
			t.Errorf("tntBreaks(%s) = %v, want %v", name, got, want)
		}
	}
}

// TestPusherKeepsTNTOffObsidian: a pusher with TNT at a bed under obsidian
// (one wool side) doesn't waste TNT on it — it digs through the wool.
func TestPusherKeepsTNTOffObsidian(t *testing.T) {
	inst, target := botWorld()
	inst.SetTNTAutoPrime(true)
	obsidian, _ := world.BlockByName("minecraft:obsidian")
	wool, _ := world.BlockByName("minecraft:white_wool")
	for _, d := range [][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 1, 0}} {
		inst.World.SetBlock(target.Add(d[0], d[1], d[2]), obsidian)
	}
	inst.World.SetBlock(target.Add(0, 0, 1), wool)
	guide := &fakeGuide{plan: game.BotPlan{
		Team:      "red",
		BlockItem: "minecraft:white_wool",
		Targets:   []world.Position{target},
	}}
	b := addTestBot(t, inst, guide, DifficultyHard, 0.5, 0.5)
	b.inv.set(hotbarStart+1, woolStack(t, 64))
	b.inv.set(hotbarStart+4, itemOf(t, "minecraft:tnt", 2))
	for i := 0; i < 2000 && inst.World.GetBlock(target) != world.Air; i++ {
		runTicks(inst, 1)
	}
	if got := inst.World.GetBlock(target); got != world.Air {
		t.Fatalf("target still standing (%s); bot at %+v", got.Name, b.bot.phys.Pos)
	}
	if n := b.countItemName("minecraft:tnt"); n != 2 {
		t.Fatalf("%d TNT left, want both: obsidian isn't worth any", n)
	}
}
