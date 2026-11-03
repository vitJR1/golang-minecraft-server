package botai

import (
	"fmt"
	"math"
	"strings"

	"minecraft-server/anticheat"
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/server/bots/botnav"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// brain.go decides what a bot does each tick. In order:
//
//  1. Fight: the nearest enemy within the difficulty's aggro range is
//     chased — bridging, building stairs up, leaping gaps — and hit
//     (reaction delay, clicks per second, hit chance). One it can't reach
//     is shot with a bow, if it has one, else left alone for a while.
//  2. Fall back home on low health (normal / hard), for a few seconds; run
//     from its own lit TNT.
//  3. Short on building blocks, or able to pay for gear it wants (a sword,
//     armour, a bow, a tool or TNT for a hard bed defence): walk to the
//     shop, buy (tactics.go).
//  4. Short and broke — or saving up for gear: stand at the resource
//     station and collect. Diamonds or emeralds piled at a generator, and
//     something it wants paid in them: go and take them (then 3. sends it
//     home to spend them).
//  5. Otherwise go for the first target (an enemy bed) — bridging across
//     the void with the blocks it carries — and dig it out, cover first;
//     cover that takes long to dig is blasted with TNT, if it has some.
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

	target    Mob
	targetAt  uint64
	nextSwing uint64
	// inReach: the target was within reach last tick; lastHealth the bot's
	// health then (a drop is a hit taken).
	inReach    bool
	lastHealth float32

	retreatUntil uint64

	route      []botnav.Step
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
	far        chan []botnav.Step
	farKey     string
	farStart   world.Position
	farRoute   bool // the current route came from a far search
	needBlocks int

	// lastRoute is a far route the bot fell off (knocked, stuck), kept to
	// pick up again (botnav.ResumeAt) rather than search afresh; resumedAt the
	// cell of the step it was last resumed at (resumed: there is one), so a
	// spot it keeps getting stuck on is searched anew after all.
	lastRoute []botnav.Step
	lastKey   string
	resumedAt world.Position
	resumed   bool
	dig       *digJob
	failed    map[world.Position]uint64
	// around is the target it's walking round to a softer side of
	// (digAround), nil when it isn't.
	around *world.Position
	// hardBlock is the cover it's stuck at (obsidian all round a bed) and
	// hardStuck that it may go home from an enemy base to buy the tool for
	// it — tier after tier until that tool digs it in time.
	hardBlock world.Block
	hardStuck bool

	// Chasing: enemies given up on (unreachable) until a tick; since when
	// the chase goal is reached with the enemy still out of reach; the last
	// chase key no route was found for.
	ignore  map[int32]uint64
	closeAt uint64
	failKey string
	failAt  uint64
	// chaseGoal is the cell the chase route was searched for.
	chaseGoal world.Position

	// Bow: the tick the draw started (0: not drawing), the next shot.
	drawAt           uint64
	nextShot         uint64
	aimYaw, aimPitch float32 // this shot's aim error
	// leadOf / leadAt / leadPos: the shot target's last seen position, for
	// its velocity (targetVelocity).
	leadOf  Mob
	leadAt  uint64
	leadPos anticheat.Vec

	badWare map[string]uint64 // wares that failed to buy, until

	// leapCell is the landing cell of the leap whose run-up is done.
	leapCell world.Position
	leapRun  bool

	// hardCover is the tool a target's cover wants, when digging it with
	// what the bot holds takes too long (world.NoTool: none).
	hardCover world.ToolType
	fleeFrom  world.Position
	fleeUntil uint64

	// pickup is the diamond / emerald spot it's raiding, while raiding
	// (tactics.go).
	pickup   world.Position
	raiding  bool
	nextRaid uint64

	// Saving up at the station for gear it can't afford yet.
	farmUntil    uint64
	farmDecideAt uint64

	role Role // its part in the team (team.go)
	// coverFail: bed-cover cells it couldn't fill, skipped until a tick.
	coverFail map[world.Position]uint64

	// Pusher (pusher.go): the next TNT drop, the tower it's building
	// (base level, the cell to fill under it, mid-jump), and how long it
	// waits at the forge for TNT.
	// Falls (reinforce.go): the last route floor, when it stood there,
	// whether the fall from it is counted; falls is the bot's own count
	// when it has no team.
	lastFloor   world.Position
	lastFloorAt uint64
	fallNoted   bool
	falls       map[world.Position]int

	nextPush   uint64
	towerBase  int
	towerCell  world.Position
	towering   bool
	pushSaveAt uint64

	yaw, pitch float32
}

// digJob is a block being dug, the tick it breaks and the last tick it was
// worked on.
type digJob struct {
	pos    world.Position
	doneAt uint64
	lastAt uint64
}

// digging is the job digging pos, carried on from last tick — nil when it
// has to start over: another block, or the dig was left off (a fight, a
// trip to the shop) and, like a player letting go of the button, lost its
// progress. Kept across the break, a job came back long "done" and broke
// the block on the spot.
func (b *Bot) digging(now uint64, pos world.Position) *digJob {
	d := b.brain.dig
	if d == nil || d.pos != pos || now > d.lastAt+1 {
		return nil
	}
	d.lastAt = now
	return d
}

// Tick is one game tick of the bot: think, move, tell everyone. The server
// runs it on the instance's tick goroutine, once the bot is Ready.
func (b *Bot) Tick(now uint64) {
	if b.body.Closed() || b.body.World() == nil {
		return
	}
	snap := b.self.Snapshot()
	if snap.Dead {
		b.phys.Vel = anticheat.Vec{}
		b.brain.route, b.brain.drawAt = nil, 0
		return
	}
	// Someone else moved the bot (respawn, a game teleport): start there.
	here := anticheat.Vec{X: snap.X, Y: snap.Y, Z: snap.Z}
	if anticheat.Dist(here, b.phys.Pos) > 0.01 {
		b.phys.Pos, b.phys.Vel = here, anticheat.Vec{}
		b.brain.route = nil
	}
	if v, ok := b.takePush(); ok {
		b.phys.Push(v.X, v.Y, v.Z)
	}

	var in botnav.Input
	if snap.Gamemode != player.Spectator {
		in = b.think(now)
	}
	b.body.SetSprinting(in.Sprint)
	b.phys.SpeedLvl = b.body.SpeedLevel()
	b.phys.JumpLvl = b.body.JumpLevel()
	if snap.Gamemode == player.Spectator || b.body.FlightExempt() {
		in.Jump = false // spectators / lobby fliers just stand
	}
	b.phys.Tick(b.world().Block, in)
	b.watchFall(now)
	if b.phys.Pos.Y < botnav.VoidY {
		b.phys.Pos.Y, b.phys.Vel.Y = botnav.VoidY, 0
	}

	p := b.phys.Pos
	moved := anticheat.Dist(p, here) > 1e-4
	turned := snap.Yaw != b.brain.yaw || snap.Pitch != b.brain.pitch
	b.body.Move(p.X, p.Y, p.Z, b.brain.yaw, b.brain.pitch, b.phys.OnGround, moved || turned)
}

// think picks this tick's input.
func (b *Bot) think(now uint64) botnav.Input {
	br := &b.brain
	w := b.world()
	guide := w.Guide()
	if guide == nil {
		return botnav.Input{}
	}
	ctx, me := w.Ctx(), b.self.Handle()
	if now >= br.planAt {
		br.plan = guide.BotPlan(ctx, me)
		br.planAt = now + botPlanEvery
		b.teamUpdate(now)
	}
	if br.plan.Idle {
		br.target, br.route, br.dig = nil, nil, nil
		return botnav.Input{}
	}

	if now%botTargetEvery == 0 || !b.targetValid(br.target) {
		b.pickTarget(now)
	}
	if now < br.fleeUntil {
		return b.flee(now)
	}
	if now < br.retreatUntil && b.onEnemyBase() {
		br.retreatUntil = 0 // at their bed: no turning back
	}
	retreating := now < br.retreatUntil
	// A pusher only fights what's in its way.
	pushing := br.role == RolePusher && br.target != nil && b.reach(br.target) > botPusherEngage
	if t := br.target; t != nil && !pushing && (!retreating || b.reach(t) < botChaseClose) {
		return b.fight(now, t)
	}
	b.stopDraw()
	if retreating {
		return b.goTo(now, "home", br.plan.Home, within(br.plan.Home, 1.5, 2), b.body.CountItem(br.plan.BlockItem))
	}
	return b.pursue(now)
}

// --- combat -----------------------------------------------------------------

// botEnemyBase is how close to an enemy bed counts as being in their base.
const botEnemyBase = 16.0

// onEnemyBase reports whether the bot is at an enemy bed that still stands:
// there it finishes the job — fights to the end, no retreat, no trips home
// to shop or to a generator — until the bed is broken.
func (b *Bot) onEnemyBase() bool {
	feet := b.body.Feet()
	for _, t := range b.brain.plan.Targets {
		if anticheat.Dist(feet, blockCentre(t)) <= botEnemyBase {
			return true
		}
	}
	return false
}

// targetValid: still in the game, alive, not spectating, still an enemy.
func (b *Bot) targetValid(t Mob) bool {
	if t == nil || !t.Valid() || !b.body.SameWorld(t) {
		return false
	}
	s := t.Snapshot()
	if s.Dead || s.Gamemode == player.Spectator {
		return false
	}
	return b.world().Guide().BotEnemy(b.world().Ctx(), b.self.Handle(), t.Handle())
}

// pickTarget engages the nearest enemy within aggro range — for an archer
// with a bow and arrows, any enemy it can see within botArcherSight.
func (b *Bot) pickTarget(now uint64) {
	br := &b.brain
	me := b.body.Feet()
	var best Mob
	sight, rise := b.skill.aggro, 4.0
	archer := b.canShoot() && br.role == RoleArcher
	if archer {
		sight, rise = botArcherSight, botArcherRise
	}
	bestD := sight
	for _, o := range b.world().Mobs() {
		if o == b.self || now < br.ignore[o.ID()] || !b.targetValid(o) {
			continue
		}
		os := o.Snapshot()
		if math.Abs(os.Y-me.Y) > rise {
			continue
		}
		d := anticheat.Dist(me, anticheat.Vec{X: os.X, Y: os.Y, Z: os.Z})
		if d > bestD {
			continue
		}
		if d > b.skill.aggro && !b.sees(o) {
			continue // far off and hidden: not worth an arrow
		}
		best, bestD = o, d
	}
	// A defender goes for whoever is at the team's bed, from any distance.
	if in := b.teamIntruder(now); in != nil {
		best = in
	}
	if best != br.target {
		br.target, br.targetAt, br.closeAt = best, now, 0
	}
}

// reach is the distance from the bot's eyes to t's hitbox.
func (b *Bot) reach(t Mob) float64 {
	s := t.Snapshot()
	box := anticheat.PlayerBox(anticheat.Sample{X: s.X, Y: s.Y, Z: s.Z, Sneaking: t.Sneaking()})
	return box.DistanceTo(b.body.Eye())
}

// fight closes in on t and swings at it.
func (b *Bot) fight(now uint64, t Mob) botnav.Input {
	br := &b.brain
	ts := t.Snapshot()
	b.lookAt(anticheat.Vec{X: ts.X, Y: ts.Y + 1.2, Z: ts.Z})

	d := b.reach(t)
	if b.skill.retreat && now >= br.retreatUntil && b.self.Snapshot().Health <= botRetreatHP && !b.onEnemyBase() && br.role != RolePusher {
		br.retreatUntil = now + botRetreatTicks
	}
	// Falling back, it fights only an enemy too close to run from (think
	// sends it here then). Dropping the target and standing still instead
	// froze it in front of the enemy: the next tick picked the same enemy
	// and came back here.
	if now < br.retreatUntil && d >= botChaseClose {
		br.target = nil
		return botnav.Input{}
	}
	if br.drawAt != 0 || (d > botArcherRange && br.role == RoleArcher) {
		// Mid-draw: finish the shot. An archer opens at range.
		if in, ok := b.shoot(now, t); ok {
			return in
		}
		if d > b.skill.aggro {
			// Seen from afar but no shot now (hidden, out of arrows): don't
			// chase them across the map.
			return b.unreachable(now, t)
		}
	}
	if d <= botAttackReach+1 {
		b.holdBestWeapon() // the sword, not the wool it bridged with
	}
	// Human reaction (reactTicks) when the enemy comes into reach and after
	// taking a hit, on top of the clicking rhythm.
	inReach := d <= botAttackReach
	if inReach && !br.inReach {
		br.nextSwing = max(br.nextSwing, now+b.reactTicks())
	}
	br.inReach = inReach
	if hp := b.self.Snapshot().Health; hp < br.lastHealth {
		br.nextSwing = max(br.nextSwing, now+b.reactTicks())
	}
	br.lastHealth = b.self.Snapshot().Health
	if inReach && now >= br.targetAt+b.skill.reaction && now >= br.nextSwing {
		br.nextSwing = now + uint64(20/b.skill.cps) + b.reactTicks()
		b.body.Swing()
		if b.rng.Float64() < b.skill.hitChance {
			b.body.Attack(t)
		}
	}
	if d <= botChaseClose {
		in := botnav.Input{}
		if b.skill.crits && b.phys.OnGround && b.rng.Float64() < 0.2 {
			in.Jump = true // fall into the next hit for a critical
		}
		return in
	}
	goal := world.Position{X: floorF(ts.X), Y: floorF(ts.Y + 0.01), Z: floorF(ts.Z)}
	key := fmt.Sprintf("chase:%d", t.ID())
	blocks := min(b.body.CountItem(br.plan.BlockItem), b.skill.chaseCap)
	in, ok := b.chase(now, key, goal, within(goal, 1.8, 1), blocks)
	if !ok {
		return b.unreachable(now, t)
	}
	if !b.placing() {
		in.Sprint = true
		b.lookAt(anticheat.Vec{X: ts.X, Y: ts.Y + 1.2, Z: ts.Z})
	}
	return in
}

const (
	botChaseDrift = 2   // the enemy this far (blocks, |dx|+|dz|) from the route's goal: search again
	botChaseRetry = 20  // ticks before a chase with no route is searched again
	botStandOff   = 30  // goal reached, enemy out of reach this long: unreachable
	botIgnoreFor  = 100 // an unreachable enemy is left alone this long
)

// chase steers toward a moving enemy with a quick search (blocks to build
// with, leaps). False when there's no way to it: no route, or the bot got as
// close as it can and still can't reach.
func (b *Bot) chase(now uint64, key string, aim world.Position, goal func(world.Position) bool, blocks int) (botnav.Input, bool) {
	br := &b.brain
	here := b.feetCell()
	if goal(here) && (br.route == nil || br.routeKey != key) {
		if br.closeAt == 0 {
			br.closeAt = now
		}
		return botnav.Input{}, now < br.closeAt+botStandOff
	}
	br.closeAt = 0
	if br.failKey == key && now < br.failAt+botChaseRetry {
		return botnav.Input{}, false
	}
	// The enemy moves all the time: the route is searched again only when
	// it has got away from where the route ends (each search is a few ms on
	// the tick).
	moved := abs(aim.X-br.chaseGoal.X)+abs(aim.Z-br.chaseGoal.Z) > botChaseDrift || aim.Y != br.chaseGoal.Y
	stale := br.routeKey != key || br.route == nil || moved || now >= br.routeAt+botRouteEvery || now >= br.progressAt+botStuckTicks
	if stale && b.phys.OnGround {
		br.chaseGoal = aim
		br.route = botnav.FindRoute(b.world().Block, here, aim, goal, b.opts(blocks, botnav.NearNodes))
		br.routeIdx, br.routeKey, br.routeAt, br.routeStart, br.progressAt, br.farRoute = 0, key, now, here, now, false
		if br.route == nil {
			br.failKey, br.failAt = key, now
			return botnav.Input{}, false
		}
	}
	if br.route == nil {
		return botnav.Input{}, true // in the air: wait to land
	}
	return b.follow(now), true
}

// unreachable: no way to t on foot. Shoot it if possible, else leave it be
// for a while.
func (b *Bot) unreachable(now uint64, t Mob) botnav.Input {
	if in, ok := b.shoot(now, t); ok {
		return in
	}
	br := &b.brain
	if br.ignore == nil {
		br.ignore = map[int32]uint64{}
	}
	br.ignore[t.ID()] = now + botIgnoreFor
	br.target, br.route = nil, nil
	return botnav.Input{}
}

// opts is the search tuning for this bot, allowing blocks to place.
func (b *Bot) opts(blocks, budget int) botnav.Opts {
	return botnav.Opts{MaxBlocks: blocks, MaxLeap: b.skill.maxLeap, LeapCost: b.style.leapCost, Budget: budget}
}

// --- goals --------------------------------------------------------------------

// pursue works through the plan when nobody's to fight.
func (b *Bot) pursue(now uint64) botnav.Input {
	br := &b.brain
	plan := br.plan
	blocks := b.body.CountItem(plan.BlockItem)
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
	// At an enemy bed: no going back for blocks, gear or resources — dig.
	if b.onEnemyBase() && !br.hardStuck {
		want, br.shopping = 0, false
	}
	// On a raid the target's route doesn't count: the bare minimum will do.
	if br.raiding {
		want = min(want, botMinBlocks)
		if br.route != nil && b.bridgesLeft() <= blocks {
			want = 0
		}
	}

	// Short of blocks and able to pay, or gear it wants within its means:
	// go shopping, and buy while the money lasts — gear first, then blocks.
	canPay := plan.HasShop && plan.Price > 0 && b.body.CountItem(plan.Currency) >= plan.Price
	ware := b.wantWare(blocks)
	if (blocks < want && canPay) || ware != nil {
		br.shopping = true
	}
	if br.shopping && (!canPay || blocks >= stockTo) && ware == nil {
		br.shopping = false
	}
	if br.shopping {
		b.endRaid(now) // off to spend it: the raid is over
		if within(plan.Shop, 2.5, 2)(b.feetCell()) {
			if now >= br.nextBuy {
				br.nextBuy = now + botBuyGap
				b.buy(ware)
			}
			b.lookAt(blockCentre(plan.Shop))
			return botnav.Input{}
		}
		return b.goTo(now, "shop", plan.Shop, within(plan.Shop, 2.5, 2), blocks)
	}
	if b.saving(now) {
		return b.goTo(now, "station", plan.Station, atStation(plan.Station), blocks)
	}
	if br.role == RoleDefender {
		if in, ok := b.defend(now, blocks); ok {
			return in
		}
		// The bed is wrapped up: off to fight with the team, over the
		// bridges already there — no waiting at the forge for blocks.
		want = 0
	}
	if blocks < want {
		if plan.HasStation {
			return b.goTo(now, "station", plan.Station, atStation(plan.Station), blocks)
		}
		return botnav.Input{}
	}
	// Diamonds / emeralds lying at a generator: go and take them (not
	// from an enemy base — the bed first — and never a pusher).
	if !b.onEnemyBase() && br.role != RolePusher {
		if in, ok := b.collect(now, blocks); ok {
			return in
		}
	}

	for _, t := range b.teamTargets() {
		if until, ok := br.failed[t]; ok && now < until {
			continue
		}
		centre := blockCentre(t)
		// Walking round a hard cover to a softer side: see it through.
		if br.around != nil && *br.around == t {
			return b.digAround(now, t)
		}
		if br.role == RolePusher && (br.towering || anticheat.Dist(b.body.Eye(), centre) <= botDigReach+1) {
			if in, ok := b.pushTNT(now, t); ok {
				return in
			}
		}
		if anticheat.Dist(b.body.Eye(), centre) <= botDigReach {
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
				if want == 0 {
					// Not going back for more (at an enemy base, a defender
					// done fortifying): this target is out of its reach for
					// now. Dropping the route only to search it again next
					// tick ran a far search every other tick, for ever.
					b.giveUp(now, t)
					br.route, br.needBlocks = nil, 0
					continue
				}
				// Not enough to get there: buy (or earn) the rest first.
				br.needBlocks = need + botBlockSpare
				br.route = nil
				return botnav.Input{}
			}
			br.needBlocks = 0
		}
		return in
	}
	if plan.HasStation {
		return b.goTo(now, "station", plan.Station, atStation(plan.Station), blocks)
	}
	return botnav.Input{}
}

// giveUp skips target t for a while.
func (b *Bot) giveUp(now uint64, t world.Position) {
	if b.brain.failed == nil {
		b.brain.failed = map[world.Position]uint64{}
	}
	b.brain.failed[t] = now + botGiveUpTicks
	if a := b.brain.around; a != nil && *a == t {
		b.brain.around = nil
	}
}

// breakTarget digs t out: whatever stands on the line from the eyes to it
// first (its cover), then t itself.
func (b *Bot) breakTarget(now uint64, t world.Position) botnav.Input {
	br := &b.brain
	at := b.world().Block
	pos := t
	if cover, ok := firstBlockOnRay(at, b.body.Eye(), blockCentre(t), t); ok {
		pos = cover
	}
	b.lookAt(blockCentre(pos))
	if b.digging(now, pos) == nil {
		blk := at(pos)
		if blk == world.Air {
			br.dig = nil
			return botnav.Input{}
		}
		b.holdBestTool(blk)
		info := world.BreakInfoFor(blk.Name)
		ticks := world.BreakTicksWith(info, b.body.HeldItem(), b.body.DigContext())
		if pos != t && !isWool(blk.Name) && (ticks < 0 || ticks > botHardDig) {
			// Hard cover (end stone, wood, obsidian…): blast it, or go and buy
			// what gets through when the bot can afford it.
			if b.blast(now, pos) {
				return botnav.Input{}
			}
			br.hardCover, br.hardBlock = info.Tool, blk
			if b.wantWare(b.body.CountItem(br.plan.BlockItem)) != nil {
				return botnav.Input{} // the shop, next tick
			}
			if ticks < 0 || ticks > botMaxDig {
				// Not with what it holds (obsidian by hand): through a
				// softer side of the bed instead.
				return b.digAround(now, t)
			}
		}
		if ticks < 0 || ticks > botMaxDig {
			b.giveUp(now, t)
			return botnav.Input{}
		}
		br.dig = &digJob{pos: pos, doneAt: now + uint64(ticks), lastAt: now}
	}
	if now%4 == 0 {
		b.body.Swing()
	}
	if now >= br.dig.doneAt {
		before := at(pos)
		b.body.Break(pos)
		if at(pos) == before { // the game refused (a map block): no way through
			b.giveUp(now, t)
		}
		br.dig = nil
	}
	return botnav.Input{}
}

// botMaxDig is the longest dig a bot takes on (ticks): obsidian by hand
// would take minutes; with a diamond pickaxe it's ~9 s.
const botMaxDig = 300

// digAround gets at bed when the cover on the bot's line to it is too hard:
// it picks the softest side of the bed — open, or the quickest block to dig
// with what the bot holds — and stands where the line to the bed runs
// through that side (digging the side block first, when it has one). No
// side it could get through: the bed is left for a while.
func (b *Bot) digAround(now uint64, bed world.Position) botnav.Input {
	br := &b.brain
	at := b.world().Block
	var side world.Position
	sideTicks := -1
	for _, d := range [5][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {0, 1, 0}} {
		n := bed.Add(d[0], d[1], d[2])
		blk := at(n)
		if strings.HasSuffix(blk.Name, "_bed") {
			continue // the bed's other half
		}
		ticks := 0
		if world.Collidable(blk) {
			_, ticks = b.bestTool(blk)
			if ticks < 0 || ticks > botMaxDig {
				continue
			}
		}
		if sideTicks < 0 || ticks < sideTicks {
			side, sideTicks = n, ticks
		}
	}
	if sideTicks < 0 {
		// Hard on every side (obsidian all round): the tool for it, from
		// the shop — even from here — if it can pay; else leave this bed.
		br.dig, br.around = nil, nil
		if hard, ok := b.softestHard(bed); ok {
			br.hardBlock, br.hardCover, br.hardStuck = hard, world.BreakInfoFor(hard.Name).Tool, true
			if b.wantWare(b.body.CountItem(br.plan.BlockItem)) != nil {
				return botnav.Input{} // shopping, next tick
			}
			br.hardStuck = false
		}
		b.giveUp(now, bed)
		return botnav.Input{}
	}
	around := bed
	br.around = &around
	if sideTicks > 0 && anticheat.Dist(b.body.Eye(), blockCentre(side)) <= botDigReach {
		return b.digBlock(now, side, bed) // open that side up
	}
	// Stand where the eyes see the bed through that side.
	centre := blockCentre(bed)
	goal := func(cell world.Position) bool {
		eye := anticheat.Vec{X: float64(cell.X) + 0.5, Y: float64(cell.Y) + anticheat.EyeHeight, Z: float64(cell.Z) + 0.5}
		if anticheat.Dist(eye, centre) > botDigReach-0.5 {
			return false
		}
		first, blocked := firstBlockOnRay(at, eye, centre, bed)
		return !blocked || first == side
	}
	if here := b.feetCell(); goal(here) && b.phys.OnGround {
		// There: through the soft side — its block first, then the bed.
		if world.Collidable(at(side)) {
			return b.digBlock(now, side, bed)
		}
		br.around = nil
		return b.digBlock(now, bed, bed)
	}
	in, status := b.goToFar(now, fmt.Sprintf("around:%v", bed), side, goal, b.body.CountItem(br.plan.BlockItem))
	if status == farFailed {
		b.giveUp(now, bed)
		br.around = nil
	}
	return in
}

// softestHard is the easiest of bed's covering blocks (sides, top) — the
// one a better tool would get through first.
func (b *Bot) softestHard(bed world.Position) (world.Block, bool) {
	at := b.world().Block
	var best world.Block
	bestHardness, found := 0.0, false
	for _, d := range [5][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {0, 1, 0}} {
		blk := at(bed.Add(d[0], d[1], d[2]))
		if !world.Collidable(blk) || strings.HasSuffix(blk.Name, "_bed") {
			continue
		}
		h := world.BreakInfoFor(blk.Name).Hardness
		if h < 0 {
			continue // unbreakable
		}
		if !found || h < bestHardness {
			best, bestHardness, found = blk, h, true
		}
	}
	return best, found
}

// digBlock digs pos (on the way to target, which is given up if the game
// refuses the break).
func (b *Bot) digBlock(now uint64, pos, target world.Position) botnav.Input {
	br := &b.brain
	at := b.world().Block
	b.lookAt(blockCentre(pos))
	if b.digging(now, pos) == nil {
		blk := at(pos)
		if blk == world.Air {
			br.dig = nil
			return botnav.Input{}
		}
		b.holdBestTool(blk)
		ticks := world.BreakTicksWith(world.BreakInfoFor(blk.Name), b.body.HeldItem(), b.body.DigContext())
		if ticks < 0 || ticks > botMaxDig {
			b.giveUp(now, target)
			return botnav.Input{}
		}
		br.dig = &digJob{pos: pos, doneAt: now + uint64(ticks), lastAt: now}
	}
	if now%4 == 0 {
		b.body.Swing()
	}
	if now >= br.dig.doneAt {
		before := at(pos)
		b.body.Break(pos)
		if at(pos) == before {
			b.giveUp(now, target)
		}
		br.dig = nil
	}
	return botnav.Input{}
}

// firstBlockOnRay walks from eye to target and returns the first block with
// collision on the way, other than skip.
func firstBlockOnRay(at botnav.BlockAt, eye, target anticheat.Vec, skip world.Position) (world.Position, bool) {
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

// goTo steers toward a goal along a route, recomputing it when the goal
// changes, when it's old, when the bot left it or got stuck. maxBlocks is how
// many blocks it may build with. A goal already reached stands still.
func (b *Bot) goTo(now uint64, key string, aim world.Position, goal func(world.Position) bool, maxBlocks int) botnav.Input {
	br := &b.brain
	here := b.feetCell()
	if goal(here) {
		br.route = nil
		return botnav.Input{}
	}
	// A goal too far for a search on the tick is in the hands of goToFar.
	if (br.far != nil && br.farKey == key) || (br.farRoute && br.routeKey == key && br.route != nil) {
		in, _ := b.goToFar(now, key, aim, goal, maxBlocks)
		return in
	}
	stale := br.routeKey != key || br.route == nil || now >= br.routeAt+botRouteEvery || now >= br.progressAt+botStuckTicks
	if stale && b.phys.OnGround {
		br.route = botnav.FindRoute(b.world().Block, here, aim, goal, b.opts(maxBlocks, botnav.NearNodes))
		br.routeIdx, br.routeKey, br.routeAt, br.routeStart, br.progressAt, br.farRoute = 0, key, now, here, now, false
		if br.route == nil {
			// Not within a quick search (another island, a long way round):
			// search off the tick.
			in, _ := b.goToFar(now, key, aim, goal, maxBlocks)
			return in
		}
	}
	if br.route == nil {
		return botnav.Input{}
	}
	return b.follow(now)
}

// dropRoute abandons the current route, keeping a far one to resume.
func (b *Bot) dropRoute() {
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
// searched on its own goroutine (botnav.FarNodes, building with up to maxBlocks) so
// a long search never stalls the instance's tick; the bot waits meanwhile.
// A route in progress is kept until it's finished, left or stuck — not
// recomputed on a timer.
func (b *Bot) goToFar(now uint64, key string, aim world.Position, goal func(world.Position) bool, maxBlocks int) (botnav.Input, int) {
	br := &b.brain
	if br.routeKey == key && br.route != nil {
		if now < br.progressAt+botStuckTicks {
			return b.follow(now), farReady
		}
		b.dropRoute() // stuck: resume from where it stands, or search anew
	}
	// Fell off a route to this goal: carry on along it if the bot is still
	// on (or by) it, instead of laying a new one from here.
	if br.lastKey == key && br.lastRoute != nil && b.phys.OnGround {
		here := b.feetCell()
		if i := botnav.ResumeAt(b.world().Block, br.lastRoute, here); i >= 0 && i < len(br.lastRoute) && !(br.resumed && br.resumedAt == br.lastRoute[i].Cell) {
			// From here: the rest of the route, its first move from this cell.
			br.route, br.routeIdx, br.routeKey, br.routeStart, br.progressAt, br.farRoute = botnav.ResumeFrom(br.lastRoute, i, here), 0, key, here, now, true
			br.resumedAt, br.resumed, br.lastRoute = br.lastRoute[i].Cell, true, nil
			return b.follow(now), farReady
		}
		br.lastRoute = nil
	}
	if br.far != nil && br.farKey == key {
		select {
		case route := <-br.far:
			br.far = nil
			if route == nil {
				return botnav.Input{}, farFailed
			}
			br.route, br.routeIdx, br.routeKey, br.routeAt, br.routeStart, br.progressAt = route, 0, key, now, br.farStart, now
			br.farRoute, br.resumed = true, false
			return b.follow(now), farReady
		default:
			return botnav.Input{}, farPending
		}
	}
	if !b.phys.OnGround {
		return botnav.Input{}, farPending
	}
	here := b.feetCell()
	if goal(here) {
		return botnav.Input{}, farReady
	}
	ch := make(chan []botnav.Step, 1)
	br.far, br.farKey, br.farStart = ch, key, here
	at := b.world().Block // MemoryWorld reads are locked: safe off the tick
	opts := b.opts(maxBlocks, botnav.FarNodes)
	go func() { ch <- botnav.FindRoute(at, here, aim, goal, opts) }()
	return botnav.Input{}, farPending
}

// bridgesLeft counts the blocks still to place on the route ahead.
func (b *Bot) bridgesLeft() int {
	br := &b.brain
	at := b.world().Block
	n := 0
	for i := br.routeIdx; i < len(br.route); i++ {
		for _, p := range br.route[i].Place {
			if world.Replaceable(at(p)) {
				n++
			}
		}
	}
	return n
}

// placing reports whether the current step still has blocks to put down.
func (b *Bot) placing() bool {
	br := &b.brain
	if br.route == nil || br.routeIdx >= len(br.route) {
		return false
	}
	_, ok := nextPlacement(b.world().Block, br.route[br.routeIdx])
	return ok
}

// nextPlacement is the first of the step's blocks not yet in place.
func nextPlacement(at botnav.BlockAt, s botnav.Step) (world.Position, bool) {
	for _, p := range s.Place {
		if world.Replaceable(at(p)) {
			return p, true
		}
	}
	return world.Position{}, false
}

// follow walks the current route one tick: toward the next cell, jumping
// up steps; building — from the centre of the cell before, the step's
// blocks in order (a bridge block under the next cell, or a stair block in
// front to jump onto) — and leaping (leap).
func (b *Bot) follow(now uint64) botnav.Input {
	br := &b.brain
	at := b.world().Block
	if br.routeIdx >= len(br.route) {
		br.route = nil
		return botnav.Input{}
	}
	step := br.route[br.routeIdx]
	prev := br.routeStart
	if br.routeIdx > 0 {
		prev = br.route[br.routeIdx-1].Cell
	}
	here := b.feetCell()
	// A hop (or a push) can carry the bot past steps: landed on a later one
	// of the route, it carries on from there. Not onto a step whose blocks
	// aren't down yet: the feet are in its cell, but the bot stands on the
	// edge of the block before, and skipping the step left it unbuilt — the
	// next move went into the void.
	if b.phys.OnGround && here != step.Cell && here != prev {
		for k := br.routeIdx + 1; k < len(br.route) && k <= br.routeIdx+botHopSkip; k++ {
			if _, unbuilt := nextPlacement(at, br.route[k]); br.route[k].Cell == here && !unbuilt {
				br.routeIdx, br.progressAt = k+1, now
				return b.follow(now)
			}
		}
	}
	runUp := prev.Add(-sign(step.Cell.X-prev.X), 0, -sign(step.Cell.Z-prev.Z))
	// Stepping off a ledge onto a lower step, the feet are still a cell or
	// more above it (on the ledge's edge): that's on the way, not off it.
	dropping := here.X == step.Cell.X && here.Z == step.Cell.Z && here.Y > step.Cell.Y && here.Y <= prev.Y
	if here != prev && here != step.Cell && !dropping && !(step.Leap && onLeapLine(prev, runUp, step.Cell, here)) && b.phys.OnGround {
		b.dropRoute() // knocked off the route: pick it up again or plan anew
		return botnav.Input{}
	}

	if b.phys.OnGround && (here == prev || here == step.Cell) {
		b.noteFloor(now, here)
		// Fell off near here before: widen the way first — along the way
		// the route goes from here (the step after, when it stands on this
		// one already; else the way it came).
		ahead := step.Cell
		if ahead == here && br.routeIdx+1 < len(br.route) {
			ahead = br.route[br.routeIdx+1].Cell
		}
		if ahead == here {
			ahead = here.Add(here.X-prev.X, 0, here.Z-prev.Z)
		}
		if in, ok := b.reinforce(now, here, ahead); ok {
			return in
		}
	}

	if next, ok := nextPlacement(at, step); ok {
		pc := cellCentre(prev)
		if here != prev || horiz(b.phys.Pos, pc) > botArrive {
			return b.toward(pc, false)
		}
		if b.world().BlockedByPlayer(next) && horiz(b.phys.Pos, pc) > 0.02 {
			// Off-centre, its own hitbox overlaps the block's cell: closer in.
			in := b.toward(pc, false)
			in.DirX, in.DirZ = in.DirX*0.3, in.DirZ*0.3
			return in
		}
		b.lookAt(blockCentre(next))
		if now >= br.nextPlace {
			br.nextPlace = now + b.skill.placeGap
			if !b.place(next, br.plan.BlockItem) {
				br.route = nil
			}
		}
		return botnav.Input{}
	}
	if step.Leap {
		return b.leap(now, prev, runUp, step)
	}

	target := cellCentre(step.Cell)
	in := b.toward(target, len(br.route)-br.routeIdx > 3 && len(step.Place) == 0)
	if step.Cell.Y > here.Y && b.phys.OnGround && horiz(b.phys.Pos, target) < 1.5 {
		in.Jump = true
	}
	// Bunny hop: sprinting down a straight, flat stretch, jump on every
	// landing — each sprint-jump adds a 0.2 push, faster than running. In
	// the air it steers for the end of the stretch and counts the cells it
	// flies over (aiming at the next cell's centre it would turn back once
	// past it).
	if in.Sprint && b.skill.hop && b.straightAhead(botHopAhead) {
		end := br.route[br.routeIdx+botHopAhead-1].Cell
		in = b.toward(cellCentre(end), true)
		if b.phys.OnGround {
			in.Jump = true
		}
		if here.X == step.Cell.X && here.Z == step.Cell.Z {
			br.routeIdx++
			br.progressAt = now
		}
		return in
	}
	b.arrive(now, step)
	return in
}

const (
	botHopAhead = 4 // straight flat steps ahead needed for a hop
	botHopSkip  = 5 // how many steps ahead a landing may pick the route up
)

// straightAhead reports whether the next n steps of the route run in one
// straight line on the bot's level, with nothing to build or leap, and room
// overhead for a jump all the way.
func (b *Bot) straightAhead(n int) bool {
	br := &b.brain
	if br.routeIdx+n > len(br.route) {
		return false
	}
	at := b.world().Block
	// From the route's own previous cell, not the bot's (mid-hop it's a
	// cell or two up).
	prev := br.routeStart
	if br.routeIdx > 0 {
		prev = br.route[br.routeIdx-1].Cell
	}
	first := br.route[br.routeIdx].Cell
	dx, dz := first.X-prev.X, first.Z-prev.Z
	if dx*dx+dz*dz != 1 { // diagonals and climbs: no
		return false
	}
	for k := br.routeIdx; k < br.routeIdx+n; k++ {
		s := br.route[k]
		if len(s.Place) > 0 || s.Leap || s.Cell.Y != prev.Y ||
			s.Cell.X-prev.X != dx || s.Cell.Z-prev.Z != dz || !botnav.HeadClear(at, s.Cell.Add(0, 2, 0)) {
			return false
		}
		prev = s.Cell
	}
	return true
}

// arrive moves on to the next step once the bot stands at this one's centre.
func (b *Bot) arrive(now uint64, step botnav.Step) {
	br := &b.brain
	if horiz(b.phys.Pos, cellCentre(step.Cell)) < botArrive && math.Abs(b.phys.Pos.Y-float64(step.Cell.Y)) < 0.6 &&
		(!step.Leap || b.phys.OnGround) {
		br.routeIdx++
		br.progressAt = now
	}
}

// leap sprint-jumps a gap: back to the run-up cell behind prev, a sprint
// at the gap, the jump off prev's far half, and steering for the landing
// cell in the air.
func (b *Bot) leap(now uint64, prev, runUp world.Position, step botnav.Step) botnav.Input {
	br := &b.brain
	target := cellCentre(step.Cell)
	if br.leapCell != step.Cell {
		br.leapCell, br.leapRun = step.Cell, false
	}
	if b.phys.OnGround && b.feetCell() != step.Cell {
		if !br.leapRun {
			rc := cellCentre(runUp)
			if horiz(b.phys.Pos, rc) > botArrive {
				return b.toward(rc, false)
			}
			br.leapRun = true
		}
		in := b.toward(target, true)
		pc := cellCentre(prev)
		dx, dz := float64(sign(step.Cell.X-prev.X)), float64(sign(step.Cell.Z-prev.Z))
		// Jump from prev's far half — before the next move would carry the
		// feet past its edge (the hitbox holds on until 0.8 past the centre).
		if (b.phys.Pos.X+b.phys.Vel.X-pc.X)*dx+(b.phys.Pos.Z+b.phys.Vel.Z-pc.Z)*dz >= 0.3 {
			in.Jump = true
		}
		return in
	}
	in := b.toward(target, true)
	if b.phys.OnGround {
		// Landed: on the cell, or carried on to the next one.
		here := b.feetCell()
		for k := br.routeIdx; k < len(br.route) && k <= br.routeIdx+1; k++ {
			if br.route[k].Cell == here {
				br.routeIdx, br.progressAt, br.leapRun = k+1, now, false
				break
			}
		}
	}
	return in
}

// onLeapLine: here is on the line of a leap from prev to land — the run-up
// cell, or a gap cell the bot's feet overhang while it still stands on prev's
// edge.
func onLeapLine(prev, runUp, land, here world.Position) bool {
	if here == runUp {
		return true
	}
	if here.Y != prev.Y {
		return false
	}
	dx, dz := sign(land.X-prev.X), sign(land.Z-prev.Z)
	return here == prev.Add(dx, 0, dz)
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// toward walks straight at p.
func (b *Bot) toward(p anticheat.Vec, sprint bool) botnav.Input {
	dx, dz := p.X-b.phys.Pos.X, p.Z-b.phys.Pos.Z
	d := math.Hypot(dx, dz)
	if d < 1e-6 {
		return botnav.Input{}
	}
	b.brain.yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	b.brain.pitch = 0
	return botnav.Input{DirX: dx / d, DirZ: dz / d, Sprint: sprint}
}

// lookAt turns the head to p.
func (b *Bot) lookAt(p anticheat.Vec) {
	e := b.body.Eye()
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

// enchantSharpness is the Sharpness enchantment's id.
const enchantSharpness = "minecraft:sharpness"

// weaponDamage is how hard a stack hits: the item's attack damage plus
// Sharpness, as handleAttack computes it.
func weaponDamage(st inventory.Stack) float32 {
	name, _ := world.ItemName(st.ID)
	d := world.AttackDamage(name)
	if lvl := st.EnchantLevel(enchantSharpness); lvl > 0 {
		d += 0.5*float32(lvl) + 0.5
	}
	return d
}

// holdBestWeapon holds the hardest-hitting stack it carries, when that beats
// what's in hand.
func (b *Bot) holdBestWeapon() {
	held := b.body.Slot(inventory.HotbarStart + int16(b.body.HeldSlot()))
	best, bestDamage := inventory.Stack{}, float32(1) // a bare hand
	if !held.Empty() {
		bestDamage = weaponDamage(held)
	}
	for s := int16(inventory.MainStart); s < inventory.HotbarStart+9; s++ {
		if st := b.body.Slot(s); !st.Empty() && weaponDamage(st) > bestDamage {
			best, bestDamage = st, weaponDamage(st)
		}
	}
	if best.Empty() {
		return
	}
	b.hold(func(st inventory.Stack) bool { return st.SameKind(best) && st.Count == best.Count })
}

// bestTool is the hotbar slot (-1: the bare hand) that digs blk fastest and
// how many ticks it takes (< 0: it can't be dug).
func (b *Bot) bestTool(blk world.Block) (int32, int) {
	info := world.BreakInfoFor(blk.Name)
	bestSlot, bestTicks := int32(-1), world.BreakTicksWith(info, "", b.body.DigContext())
	for k := int32(0); k < 9; k++ {
		st := b.body.Slot(int16(inventory.HotbarStart) + int16(k))
		if st.Empty() {
			continue
		}
		name, _ := world.ItemName(st.ID)
		if t := world.BreakTicksWith(info, name, b.body.DigContext()); t >= 0 && (bestTicks < 0 || t < bestTicks) {
			bestSlot, bestTicks = k, t
		}
	}
	return bestSlot, bestTicks
}

// holdBestTool holds whatever digs blk fastest (the hand included).
func (b *Bot) holdBestTool(blk world.Block) {
	bestSlot, _ := b.bestTool(blk)
	if bestSlot >= 0 && b.body.HeldSlot() != bestSlot {
		b.body.SelectSlot(bestSlot)
	}
}

// hold selects a hotbar slot holding a stack that matches, swapping one in
// from the main inventory if needed. False when there's none.
func (b *Bot) hold(match func(inventory.Stack) bool) bool {
	for k := int16(0); k < 9; k++ {
		if st := b.body.Slot(inventory.HotbarStart + k); !st.Empty() && match(st) {
			if b.body.HeldSlot() != int32(k) {
				b.body.SelectSlot(int32(k))
			}
			return true
		}
	}
	for s := int16(inventory.MainStart); s < inventory.HotbarStart; s++ {
		if st := b.body.Slot(s); !st.Empty() && match(st) {
			b.body.SwapSlots(s, inventory.HotbarStart+8)
			b.body.SelectSlot(8)
			return true
		}
	}
	return false
}

// placeWith holds the first block matching and places it at pos.
func (b *Bot) placeWith(pos world.Position, match func(item string) bool) bool {
	if !b.hold(func(st inventory.Stack) bool {
		name, _ := world.ItemName(st.ID)
		return match(name)
	}) {
		return false
	}
	return b.body.Place(pos)
}

// place puts the bot's building block (item, or any wool) at pos, through
// the same rules as a player's placement: replaceable spot, nobody in the
// way, the game's rewrite and veto.
func (b *Bot) place(pos world.Position, item string) bool {
	return b.placeWith(pos, func(name string) bool { return name == item || isWool(name) })
}

// isWool reports whether an item id is any colour of wool.
func isWool(item string) bool { return strings.HasSuffix(item, "_wool") }
