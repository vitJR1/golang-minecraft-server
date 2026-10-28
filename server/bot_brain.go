package server

import (
	"fmt"
	"math"
	"strings"

	"minecraft-server/anticheat"
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// bot_brain.go decides what a bot does each tick. In order:
//
//  1. Fight: the nearest enemy within the difficulty's aggro range is
//     chased and hit (reaction delay, clicks per second, hit chance).
//  2. Fall back home on low health (normal / hard), for a few seconds.
//  3. Short on building blocks and able to pay: walk to the shop, buy.
//  4. Short and broke: stand at the resource station and collect.
//  5. Otherwise go for the first target (an enemy bed) — bridging across
//     the void with the blocks it carries — and dig it out, cover first.
//
// What counts as an enemy, where the station / shop / targets are and how to
// buy all come from the game (game.BotGuide).

const (
	botPlanEvery    = 10  // ticks between asking the game for a plan
	botTargetEvery  = 5   // ticks between looking for enemies
	botRouteEvery   = 100 // a route older than this is recomputed
	botStuckTicks   = 60  // no progress for this long: recompute
	botMinBlocks    = 16  // fewer blocks than this: get more before setting out
	botWantBlocks   = 48  // buy up to this many
	botBuyGap       = 6   // ticks between purchases
	botRetreatHP    = 6   // health at which normal/hard bots fall back
	botRetreatTicks = 100 // how long a fallback lasts
	botGiveUpTicks  = 400 // a target found unreachable is skipped this long
	botDigReach     = 4.0 // eye to block centre for digging (vanilla 4.5)
	botBridgeCap    = 256 // most blocks a route to a target may bridge with
	botBlockSpare   = 8   // blocks bought beyond what a route needs
	botAttackReach  = 3.0 // to the victim's hitbox (the client's own limit)
	botChaseClose   = 2.2 // stop closing in at this distance
	botArrive       = 0.3 // within this of a cell's centre counts as there
)

// brainState is what a bot remembers between ticks.
type brainState struct {
	plan   game.BotPlan
	planAt uint64

	target    *ClientConnection
	targetAt  uint64
	nextSwing uint64

	retreatUntil uint64

	route      []routeStep
	routeIdx   int
	routeKey   string
	routeAt    uint64
	routeStart world.Position
	progressAt uint64

	nextPlace uint64
	nextBuy   uint64
	shopping  bool // buying up to botWantBlocks once it went to the shop

	// far is an island-to-island route being searched off the tick
	// (goToFar), for farKey from farStart; needBlocks is how many blocks the
	// chosen target's route takes, so the bot buys that many first.
	far        chan []routeStep
	farKey     string
	farStart   world.Position
	farRoute   bool // the current route came from a far search
	needBlocks int

	// lastRoute is a far route the bot fell off (knocked, stuck), kept to
	// pick up again (resumeAt) rather than search afresh; resumedAt the step
	// it was last resumed at, so a spot it keeps getting stuck on is searched
	// anew after all.
	lastRoute []routeStep
	lastKey   string
	resumedAt int
	dig       *digJob
	failed    map[world.Position]uint64

	yaw, pitch float32
}

// digJob is a block being dug and the tick it breaks.
type digJob struct {
	pos    world.Position
	doneAt uint64
}

// botTick runs every bot in the instance (registered by NewInstance).
func (i *Instance) botTick(tick uint64) {
	i.Players.Range(func(c *ClientConnection) {
		if c.isBot() && c.bot.ready.Load() {
			c.bot.tick(tick)
		}
	})
}

// tick is one game tick of the bot: think, move, tell everyone.
func (b *bot) tick(now uint64) {
	c := b.c
	if c.player == nil || c.instance == nil || c.isClosed() {
		return
	}
	snap := c.player.Snapshot()
	if snap.Dead {
		b.phys.Vel = anticheat.Vec{}
		b.brain.route = nil
		return
	}
	// Someone else moved the bot (respawn, a game teleport): start there.
	here := anticheat.Vec{X: snap.X, Y: snap.Y, Z: snap.Z}
	if anticheat.Dist(here, b.phys.Pos) > 0.01 {
		b.phys.Pos, b.phys.Vel = here, anticheat.Vec{}
		b.brain.route = nil
	}
	if v, ok := b.takePush(); ok {
		b.phys.push(v.X, v.Y, v.Z)
	}

	var in physInput
	if snap.Gamemode != player.Spectator {
		in = b.think(now)
	}
	c.ac.Sprinting.Store(in.Sprint)
	b.phys.SpeedLvl = c.effectLevel(EffectSpeed)
	b.phys.JumpLvl = c.effectLevel(EffectJumpBoost)
	if snap.Gamemode == player.Spectator || c.flightExempt() {
		in.Jump = false // spectators / lobby fliers just stand
	}
	b.phys.step(c.instance.World.GetBlock, in)
	if b.phys.Pos.Y < botVoidY {
		b.phys.Pos.Y, b.phys.Vel.Y = botVoidY, 0
	}

	p := b.phys.Pos
	moved := anticheat.Dist(p, here) > 1e-4
	turned := snap.Yaw != b.brain.yaw || snap.Pitch != b.brain.pitch
	c.player.MoveAndLook(p.X, p.Y, p.Z, b.brain.yaw, b.brain.pitch, b.phys.OnGround)
	if moved || turned {
		c.broadcastEntityTeleport()
		c.broadcastHeadRotation()
	}
}

// think picks this tick's input.
func (b *bot) think(now uint64) physInput {
	c, br := b.c, &b.brain
	guide := c.instance.botGuide
	if guide == nil {
		return physInput{}
	}
	ctx, me := c.instance.pluginCtx(), playerBridge{conn: c}
	if now >= br.planAt {
		br.plan = guide.BotPlan(ctx, me)
		br.planAt = now + botPlanEvery
	}
	if br.plan.Idle {
		br.target, br.route, br.dig = nil, nil, nil
		return physInput{}
	}

	if now%botTargetEvery == 0 || !b.targetValid(br.target) {
		b.pickTarget(now)
	}
	retreating := now < br.retreatUntil
	if t := br.target; t != nil && (!retreating || b.reach(t) < botChaseClose) {
		return b.fight(now, t)
	}
	if retreating {
		return b.goTo(now, "home", br.plan.Home, within(br.plan.Home, 1.5, 2), b.c.countItemName(br.plan.BlockItem))
	}
	return b.pursue(now)
}

// --- combat -----------------------------------------------------------------

// targetValid: still in the game, alive, not spectating, still an enemy.
func (b *bot) targetValid(t *ClientConnection) bool {
	if t == nil || t.isClosed() || t.instance != b.c.instance || t.player == nil {
		return false
	}
	s := t.player.Snapshot()
	if s.Dead || s.Gamemode == player.Spectator {
		return false
	}
	return b.c.instance.botGuide.BotEnemy(b.c.instance.pluginCtx(), playerBridge{conn: b.c}, playerBridge{conn: t})
}

// pickTarget engages the nearest enemy within aggro range.
func (b *bot) pickTarget(now uint64) {
	c, br := b.c, &b.brain
	me := c.feet()
	var best *ClientConnection
	bestD := b.skill.aggro
	for _, o := range c.instance.Players.snapshot() {
		if o == c || !b.targetValid(o) {
			continue
		}
		os := o.player.Snapshot()
		if math.Abs(os.Y-me.Y) > 4 {
			continue
		}
		if d := anticheat.Dist(me, anticheat.Vec{X: os.X, Y: os.Y, Z: os.Z}); d <= bestD {
			best, bestD = o, d
		}
	}
	if best != br.target {
		br.target, br.targetAt = best, now
	}
}

// reach is the distance from the bot's eyes to t's hitbox.
func (b *bot) reach(t *ClientConnection) float64 {
	s := t.player.Snapshot()
	box := anticheat.PlayerBox(anticheat.Sample{X: s.X, Y: s.Y, Z: s.Z, Sneaking: t.ac.Sneaking.Load()})
	return box.DistanceTo(b.c.eye())
}

// fight closes in on t and swings at it.
func (b *bot) fight(now uint64, t *ClientConnection) physInput {
	c, br := b.c, &b.brain
	ts := t.player.Snapshot()
	b.lookAt(anticheat.Vec{X: ts.X, Y: ts.Y + 1.2, Z: ts.Z})

	if b.skill.retreat && c.player.Health() <= botRetreatHP {
		br.retreatUntil = now + botRetreatTicks
		br.target = nil
		return physInput{}
	}

	d := b.reach(t)
	if d <= botAttackReach+1 {
		b.holdBestWeapon() // the sword, not the wool it bridged with
	}
	if d <= botAttackReach && now >= br.targetAt+b.skill.reaction && now >= br.nextSwing {
		br.nextSwing = now + uint64(20/b.skill.cps) + uint64(b.rng.Intn(2))
		c.broadcastEntityAnimation(0)
		if b.rng.Float64() < b.skill.hitChance && c.instance.allowAttack(c, t) {
			c.handleAttack(t)
		}
	}
	if d <= botChaseClose {
		in := physInput{}
		if b.skill.crits && b.phys.OnGround && b.rng.Float64() < 0.2 {
			in.Jump = true // fall into the next hit for a critical
		}
		return in
	}
	goal := world.Position{X: floorF(ts.X), Y: floorF(ts.Y + 0.01), Z: floorF(ts.Z)}
	key := fmt.Sprintf("chase:%d:%v", t.player.EntityID, goal)
	in := b.goTo(now, key, goal, within(goal, 1.8, 1), 0)
	in.Sprint = true
	b.lookAt(anticheat.Vec{X: ts.X, Y: ts.Y + 1.2, Z: ts.Z})
	return in
}

// --- goals --------------------------------------------------------------------

// pursue works through the plan when nobody's to fight.
func (b *bot) pursue(now uint64) physInput {
	c, br := b.c, &b.brain
	plan := br.plan
	blocks := c.countItemName(plan.BlockItem)
	if len(plan.Targets) == 0 {
		br.needBlocks = 0
	}
	// How many blocks to carry before setting out: the minimum, or what the
	// target's route takes. Once at the shop, stock up a little beyond.
	want := max(botMinBlocks, br.needBlocks)
	stockTo := max(botWantBlocks, want)
	// On the way to a target, only the bridges still ahead matter: carry on
	// while the blocks cover them.
	if br.route != nil && strings.HasPrefix(br.routeKey, "target:") && b.bridgesLeft() <= blocks {
		want = 0
	}

	// Short of blocks and able to pay: go shopping, and buy while the money
	// lasts.
	canPay := plan.HasShop && plan.Price > 0 && c.countItemName(plan.Currency) >= plan.Price
	if blocks < want && canPay {
		br.shopping = true
	}
	if br.shopping && (!canPay || blocks >= stockTo) {
		br.shopping = false
	}
	if br.shopping {
		if within(plan.Shop, 2.5, 2)(c.feetCellNow()) {
			if now >= br.nextBuy {
				br.nextBuy = now + botBuyGap
				c.instance.botGuide.BotBuy(c.instance.pluginCtx(), playerBridge{conn: c}, plan.BuyKey)
			}
			b.lookAt(blockCentre(plan.Shop))
			return physInput{}
		}
		return b.goTo(now, "shop", plan.Shop, within(plan.Shop, 2.5, 2), blocks)
	}
	if blocks < want {
		if plan.HasStation {
			return b.goTo(now, "station", plan.Station, atStation(plan.Station), blocks)
		}
		return physInput{}
	}

	for _, t := range plan.Targets {
		if until, ok := br.failed[t]; ok && now < until {
			continue
		}
		centre := blockCentre(t)
		if anticheat.Dist(c.eye(), centre) <= botDigReach {
			return b.breakTarget(now, t)
		}
		goal := func(p world.Position) bool {
			eye := anticheat.Vec{X: float64(p.X) + 0.5, Y: float64(p.Y) + anticheat.EyeHeight, Z: float64(p.Z) + 0.5}
			return anticheat.Dist(eye, centre) <= botDigReach-0.5
		}
		in, status := b.goToFar(now, fmt.Sprintf("target:%v", t), t, goal, botBridgeCap)
		switch status {
		case farFailed:
			b.giveUp(now, t)
			br.needBlocks = 0
			continue
		case farReady:
			if need := b.bridgesLeft(); need > blocks {
				// Not enough to get there: buy (or earn) the rest first.
				br.needBlocks = need + botBlockSpare
				br.route = nil
				return physInput{}
			}
			br.needBlocks = 0
		}
		return in
	}
	if plan.HasStation {
		return b.goTo(now, "station", plan.Station, atStation(plan.Station), blocks)
	}
	return physInput{}
}

// giveUp skips target t for a while.
func (b *bot) giveUp(now uint64, t world.Position) {
	if b.brain.failed == nil {
		b.brain.failed = map[world.Position]uint64{}
	}
	b.brain.failed[t] = now + botGiveUpTicks
}

// breakTarget digs t out: whatever stands on the line from the eyes to it
// first (its cover), then t itself.
func (b *bot) breakTarget(now uint64, t world.Position) physInput {
	c, br := b.c, &b.brain
	at := c.instance.World.GetBlock
	pos := t
	if cover, ok := firstBlockOnRay(at, c.eye(), blockCentre(t), t); ok {
		pos = cover
	}
	b.lookAt(blockCentre(pos))
	if br.dig == nil || br.dig.pos != pos {
		blk := at(pos)
		if blk == world.Air {
			br.dig = nil
			return physInput{}
		}
		b.holdBestTool(blk)
		ticks := world.BreakTicksWith(world.BreakInfoFor(blk.Name), c.heldItemName(), c.digContext())
		if ticks < 0 {
			b.giveUp(now, t)
			return physInput{}
		}
		br.dig = &digJob{pos: pos, doneAt: now + uint64(ticks)}
	}
	if now%4 == 0 {
		c.broadcastEntityAnimation(0)
	}
	if now >= br.dig.doneAt {
		before := at(pos)
		c.breakBlock(pos, true)
		if at(pos) == before { // the game refused (a map block): no way through
			b.giveUp(now, t)
		}
		br.dig = nil
	}
	return physInput{}
}

// firstBlockOnRay walks from eye to target and returns the first block with
// collision on the way, other than skip.
func firstBlockOnRay(at blockAt, eye, target anticheat.Vec, skip world.Position) (world.Position, bool) {
	d := anticheat.Dist(eye, target)
	steps := int(d / 0.1)
	for k := 1; k < steps; k++ {
		f := float64(k) / float64(steps)
		p := world.Position{
			X: floorF(eye.X + (target.X-eye.X)*f),
			Y: floorF(eye.Y + (target.Y-eye.Y)*f),
			Z: floorF(eye.Z + (target.Z-eye.Z)*f),
		}
		if p == skip {
			return world.Position{}, false
		}
		if world.Collidable(at(p)) {
			return p, true
		}
	}
	return world.Position{}, false
}

// --- movement -------------------------------------------------------------------

// within is a goal: the cell lies within r blocks of p horizontally and at
// most up blocks above or below it.
func within(p world.Position, r float64, up int) func(world.Position) bool {
	return func(c world.Position) bool {
		dx, dz := float64(c.X-p.X), float64(c.Z-p.Z)
		dy := c.Y - p.Y
		return math.Hypot(dx, dz) <= r && dy <= up && dy >= -up
	}
}

// atStation is the goal for collecting: next to the generator and at its
// level — a step above it the drops land out of pickup reach.
func atStation(p world.Position) func(world.Position) bool { return within(p, 1.0, 0) }

// feetCellNow is the cell the bot's feet are in.
func (c *ClientConnection) feetCellNow() world.Position {
	p := c.bot.phys.Pos
	return feetCell(p.X, p.Y, p.Z)
}

// goTo steers toward a goal along a route, recomputing it when the goal
// changes, when it's old, when the bot left it or got stuck. maxBridge is how
// many blocks it may bridge with. A goal already reached stands still.
func (b *bot) goTo(now uint64, key string, aim world.Position, goal func(world.Position) bool, maxBridge int) physInput {
	c, br := b.c, &b.brain
	here := c.feetCellNow()
	if goal(here) {
		br.route = nil
		return physInput{}
	}
	// A goal too far for a search on the tick is in the hands of goToFar.
	if (br.far != nil && br.farKey == key) || (br.farRoute && br.routeKey == key && br.route != nil) {
		in, _ := b.goToFar(now, key, aim, goal, maxBridge)
		return in
	}
	stale := br.routeKey != key || br.route == nil || now >= br.routeAt+botRouteEvery || now >= br.progressAt+botStuckTicks
	if stale && b.phys.OnGround {
		br.route = findRoute(c.instance.World.GetBlock, here, aim, goal, maxBridge, pathNearNodes)
		br.routeIdx, br.routeKey, br.routeAt, br.routeStart, br.progressAt, br.farRoute = 0, key, now, here, now, false
		if br.route == nil {
			// Not within a quick search (another island, a long way round):
			// search off the tick.
			in, _ := b.goToFar(now, key, aim, goal, maxBridge)
			return in
		}
	}
	if br.route == nil {
		return physInput{}
	}
	return b.follow(now)
}

// dropRoute abandons the current route, keeping a far one to resume.
func (b *bot) dropRoute() {
	br := &b.brain
	if br.farRoute && br.route != nil {
		br.lastRoute, br.lastKey = br.route, br.routeKey
	}
	br.route = nil
}

// Far-route search states (goToFar).
const (
	farPending = iota // searching (or waiting to stand still to search)
	farReady          // following a route
	farFailed         // no route
)

// goToFar steers toward a goal that may be islands away. The route is
// searched on its own goroutine (pathFarNodes, bridging up to maxBridge) so
// a long search never stalls the instance's tick; the bot waits meanwhile.
// A route in progress is kept until it's finished, left or stuck — not
// recomputed on a timer.
func (b *bot) goToFar(now uint64, key string, aim world.Position, goal func(world.Position) bool, maxBridge int) (physInput, int) {
	c, br := b.c, &b.brain
	if br.routeKey == key && br.route != nil {
		if now < br.progressAt+botStuckTicks {
			return b.follow(now), farReady
		}
		b.dropRoute() // stuck: resume from where it stands, or search anew
	}
	// Fell off a route to this goal: carry on along it if the bot is still
	// on (or by) it, instead of laying a new one from here.
	if br.lastKey == key && br.lastRoute != nil && b.phys.OnGround {
		here := c.feetCellNow()
		if i := resumeAt(br.lastRoute, here); i >= 0 && i < len(br.lastRoute) && i != br.resumedAt {
			br.route, br.routeIdx, br.routeKey, br.routeStart, br.progressAt, br.farRoute = br.lastRoute, i, key, here, now, true
			br.resumedAt, br.lastRoute = i, nil
			return b.follow(now), farReady
		}
		br.lastRoute = nil
	}
	if br.far != nil && br.farKey == key {
		select {
		case route := <-br.far:
			br.far = nil
			if route == nil {
				return physInput{}, farFailed
			}
			br.route, br.routeIdx, br.routeKey, br.routeAt, br.routeStart, br.progressAt = route, 0, key, now, br.farStart, now
			br.farRoute, br.resumedAt = true, -1
			return b.follow(now), farReady
		default:
			return physInput{}, farPending
		}
	}
	if !b.phys.OnGround {
		return physInput{}, farPending
	}
	here := c.feetCellNow()
	if goal(here) {
		return physInput{}, farReady
	}
	ch := make(chan []routeStep, 1)
	br.far, br.farKey, br.farStart = ch, key, here
	at := c.instance.World.GetBlock // MemoryWorld reads are locked: safe off the tick
	go func() { ch <- findRoute(at, here, aim, goal, maxBridge, pathFarNodes) }()
	return physInput{}, farPending
}

// bridgesLeft counts the bridge steps still ahead on the route.
func (b *bot) bridgesLeft() int {
	br := &b.brain
	at := b.c.instance.World.GetBlock
	n := 0
	for i := br.routeIdx; i < len(br.route); i++ {
		if br.route[i].Bridge && !groundUnder(at, br.route[i].Cell) {
			n++
		}
	}
	return n
}

// follow walks the current route one tick: toward the next cell, jumping
// up steps, and bridging — from the centre of the cell before a gap, a block
// under the next cell, then the step.
func (b *bot) follow(now uint64) physInput {
	c, br := b.c, &b.brain
	at := c.instance.World.GetBlock
	if br.routeIdx >= len(br.route) {
		br.route = nil
		return physInput{}
	}
	step := br.route[br.routeIdx]
	prev := br.routeStart
	if br.routeIdx > 0 {
		prev = br.route[br.routeIdx-1].Cell
	}
	here := c.feetCellNow()
	if here != prev && here != step.Cell && b.phys.OnGround {
		b.dropRoute() // knocked off the route: pick it up again or plan anew
		return physInput{}
	}

	if step.Bridge && !groundUnder(at, step.Cell) {
		pc := cellCentre(prev)
		if horiz(b.phys.Pos, pc) > botArrive {
			return b.toward(pc, false)
		}
		floor := step.Cell.Add(0, -1, 0)
		b.lookAt(blockCentre(floor))
		if now >= br.nextPlace {
			br.nextPlace = now + b.skill.placeGap
			if !c.botPlace(floor, br.plan.BlockItem) {
				br.route = nil
			}
		}
		return physInput{}
	}

	target := cellCentre(step.Cell)
	in := b.toward(target, len(br.route)-br.routeIdx > 3 && !step.Bridge)
	if step.Cell.Y > here.Y && b.phys.OnGround && horiz(b.phys.Pos, target) < 1.5 {
		in.Jump = true
	}
	if horiz(b.phys.Pos, target) < botArrive && math.Abs(b.phys.Pos.Y-float64(step.Cell.Y)) < 0.6 {
		br.routeIdx++
		br.progressAt = now
	}
	return in
}

// toward walks straight at p.
func (b *bot) toward(p anticheat.Vec, sprint bool) physInput {
	dx, dz := p.X-b.phys.Pos.X, p.Z-b.phys.Pos.Z
	d := math.Hypot(dx, dz)
	if d < 1e-6 {
		return physInput{}
	}
	b.brain.yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	b.brain.pitch = 0
	return physInput{DirX: dx / d, DirZ: dz / d, Sprint: sprint}
}

// lookAt turns the head to p.
func (b *bot) lookAt(p anticheat.Vec) {
	e := b.c.eye()
	dx, dy, dz := p.X-e.X, p.Y-e.Y, p.Z-e.Z
	b.brain.yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	b.brain.pitch = float32(-math.Atan2(dy, math.Hypot(dx, dz)) * 180 / math.Pi)
}

func cellCentre(p world.Position) anticheat.Vec {
	return anticheat.Vec{X: float64(p.X) + 0.5, Y: float64(p.Y), Z: float64(p.Z) + 0.5}
}

func blockCentre(p world.Position) anticheat.Vec { return anticheat.BlockCentre(p.X, p.Y, p.Z) }

func horiz(a, b anticheat.Vec) float64 { return math.Hypot(a.X-b.X, a.Z-b.Z) }

// --- hands --------------------------------------------------------------------

// countItemName counts an item by id ("" or unknown → 0).
func (c *ClientConnection) countItemName(item string) int {
	id, ok := world.ItemByName(item)
	if !ok {
		return 0
	}
	return c.countItem(id)
}

// botHold selects a hotbar slot holding a stack that matches, swapping one
// in from the main inventory if needed. False when there's none.
func (c *ClientConnection) botHold(match func(itemStack) bool) bool {
	for k := int16(0); k < 9; k++ {
		if st := c.inv.get(hotbarStart + k); !st.empty() && match(st) {
			if c.heldSlot.Load() != int32(k) {
				c.heldSlot.Store(int32(k))
				c.equipmentChanged()
			}
			return true
		}
	}
	for s := int16(mainInvStart); s < hotbarStart; s++ {
		if st := c.inv.get(s); !st.empty() && match(st) {
			last := int16(hotbarStart + 8)
			c.inv.slots[s], c.inv.slots[last] = c.inv.slots[last], c.inv.slots[s]
			c.heldSlot.Store(8)
			c.equipmentChanged()
			return true
		}
	}
	return false
}

// weaponDamage is how hard a stack hits: the item's attack damage plus
// Sharpness, as handleAttack computes it.
func weaponDamage(st itemStack) float32 {
	name, _ := world.ItemName(st.ID)
	d := world.AttackDamage(name)
	if lvl := st.enchantLevel(EnchantSharpness); lvl > 0 {
		d += 0.5*float32(lvl) + 0.5
	}
	return d
}

// holdBestWeapon holds the hardest-hitting stack it carries, when that beats
// what's in hand.
func (b *bot) holdBestWeapon() {
	c := b.c
	held := c.inv.held(c.heldSlot.Load())
	best, bestDamage := itemStack{}, float32(1) // a bare hand
	if !held.empty() {
		bestDamage = weaponDamage(held)
	}
	for s := int16(mainInvStart); s < hotbarStart+9; s++ {
		if st := c.inv.get(s); !st.empty() && weaponDamage(st) > bestDamage {
			best, bestDamage = st, weaponDamage(st)
		}
	}
	if best.empty() {
		return
	}
	c.botHold(func(st itemStack) bool { return st.sameKind(best) && st.Count == best.Count })
}

// holdBestTool holds whatever digs blk fastest (the hand included).
func (b *bot) holdBestTool(blk world.Block) {
	c := b.c
	info := world.BreakInfoFor(blk.Name)
	bestSlot, bestTicks := int32(-1), world.BreakTicksWith(info, "", c.digContext())
	for k := int32(0); k < 9; k++ {
		st := c.inv.get(int16(hotbarStart) + int16(k))
		if st.empty() {
			continue
		}
		name, _ := world.ItemName(st.ID)
		if t := world.BreakTicksWith(info, name, c.digContext()); t >= 0 && (bestTicks < 0 || t < bestTicks) {
			bestSlot, bestTicks = k, t
		}
	}
	if bestSlot >= 0 && c.heldSlot.Load() != bestSlot {
		c.heldSlot.Store(bestSlot)
		c.equipmentChanged()
	}
}

// botPlace puts the bot's building block (item, or any wool) at pos, through
// the same rules as a player's placement: replaceable spot, nobody in the
// way, the game's rewrite and veto.
func (c *ClientConnection) botPlace(pos world.Position, item string) bool {
	if !c.botHold(func(st itemStack) bool {
		name, _ := world.ItemName(st.ID)
		return name == item || isWool(name)
	}) {
		return false
	}
	block, ok := world.BlockByName(c.heldItemName())
	inst := c.instance
	if !ok || !world.Replaceable(inst.World.GetBlock(pos)) || inst.blockedByPlayer(pos) {
		return false
	}
	block = inst.rewritePlacedBlock(c, pos, block)
	if !inst.allowBlockPlace(c, pos, block) {
		return false
	}
	inst.SetBlock(pos, block)
	c.consumeHeld()
	c.broadcastEntityAnimation(0)
	return true
}
