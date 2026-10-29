package server

import (
	"fmt"
	"math"

	"minecraft-server/anticheat"
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/world"
)

// bot_tactics.go: what a bot does beyond walking and hitting — shooting an
// enemy it can't walk to, choosing gear in the shop (game.BotPlan.Wares),
// saving up for it at the station, and blasting a hard bed defence with TNT.

const (
	botHardDig    = 60   // cover that takes longer than this to dig is hard
	botHomeRange  = 24.0 // within this of home the bot thinks about gear
	botTNTFlee    = 70   // ticks to keep away from its own TNT
	botFleeDist   = 7.0  // how far from it
	botBowRange   = 40.0 // farthest shot (an archer's: botArcherSight)
	botDrawTicks  = 22   // a full draw (force 1 at 20)
	botFarmEvery  = 1200 // ticks between "save up for gear?" decisions
	botWareRetry  = 200  // a purchase that failed isn't tried again this long
	botArrowStock = 16   // arrows it keeps
	itemTNT       = "minecraft:tnt"
	itemBow       = "minecraft:bow"
	itemArrow     = "minecraft:arrow"
)

// --- bow ------------------------------------------------------------------------

// shoot draws the bow at t and looses when drawn, aiming the arc. False when
// it can't (no bow or arrows, out of range, no line of sight).
func (b *bot) shoot(now uint64, t *ClientConnection) (physInput, bool) {
	c, br := b.c, &b.brain
	if c.countItemName(itemBow) == 0 || c.countItemName(itemArrow) == 0 {
		b.stopDraw()
		return physInput{}, false
	}
	ts := t.player.Snapshot()
	aim := anticheat.Vec{X: ts.X, Y: ts.Y + 1.0, Z: ts.Z}
	from := c.eye()
	from.Y -= 0.1 // arrows leave at eye height − 0.1
	reach := botBowRange
	if br.role == roleArcher {
		reach = botArcherSight
	}
	if anticheat.Dist(from, aim) > reach || !b.sees(t) {
		b.stopDraw()
		return physInput{}, false
	}
	// Lead a moving target: where it will be when the arrow gets there
	// (its velocity from the last ticks × the flight time), aimed twice.
	vel := b.targetVelocity(now, t)
	yaw, pitch, flight, ok := aimArrow(from, aim)
	if ok && (vel.X != 0 || vel.Z != 0) {
		lead := anticheat.Vec{X: aim.X + vel.X*float64(flight), Y: aim.Y, Z: aim.Z + vel.Z*float64(flight)}
		if y2, p2, _, ok2 := aimArrow(from, lead); ok2 {
			yaw, pitch = y2, p2
		}
	}
	if !ok {
		b.stopDraw()
		return physInput{}, false
	}
	// A teammate in the line of fire would catch the arrow: hold the shot
	// and step aside to clear the line.
	if f := b.friendInLine(from, aim); f != nil {
		return b.sidestep(f, aim), true
	}
	if br.drawAt == 0 {
		if now >= br.nextShot {
			if !c.botHold(func(st itemStack) bool { n, _ := world.ItemName(st.ID); return n == itemBow }) {
				return physInput{}, false
			}
			c.startDraw()
			if c.using.Load() == nil {
				return physInput{}, false
			}
			br.drawAt = now
			br.aimYaw = float32(b.rng.NormFloat64() * b.skill.aimNoise)
			br.aimPitch = float32(b.rng.NormFloat64() * b.skill.aimNoise)
		}
	}
	br.yaw, br.pitch = yaw+br.aimYaw, pitch+br.aimPitch
	if br.drawAt != 0 && now >= br.drawAt+botDrawTicks {
		c.player.LookAt(br.yaw, br.pitch, b.phys.OnGround)
		c.releaseUse()
		br.drawAt = 0
		br.nextShot = now + 10 + uint64(b.rng.Intn(20))
	}
	return physInput{}, true
}

// friendInLine returns a player who isn't an enemy standing (inflated like
// an arrow's hit test) on the straight line from → to, or nil.
func (b *bot) friendInLine(from, to anticheat.Vec) *ClientConnection {
	c := b.c
	guide, ctx, me := c.instance.botGuide, c.instance.pluginCtx(), playerBridge{conn: c}
	for _, o := range c.instance.Players.snapshot() {
		if o == c || o.player == nil {
			continue
		}
		s := o.player.Snapshot()
		if s.Dead || s.Gamemode == player.Spectator || guide.BotEnemy(ctx, me, playerBridge{conn: o}) {
			continue
		}
		const half, pad = 0.3, arrowHitInflate
		if _, hit := segmentHitsBox(from.X, from.Y, from.Z, to.X, to.Y, to.Z,
			s.X-half-pad, s.Y-pad, s.Z-half-pad, s.X+half+pad, s.Y+1.8+pad, s.Z+half+pad); hit {
			return o
		}
	}
	return nil
}

// sidestep walks sideways off the line to aim, away from friend f.
func (b *bot) sidestep(f *ClientConnection, aim anticheat.Vec) physInput {
	p := b.phys.Pos
	dx, dz := aim.X-p.X, aim.Z-p.Z
	d := math.Hypot(dx, dz)
	if d < 1e-6 {
		return physInput{}
	}
	sx, sz := -dz/d, dx/d // perpendicular to the line
	fs := f.player.Snapshot()
	if (fs.X-p.X)*sx+(fs.Z-p.Z)*sz > 0 {
		sx, sz = -sx, -sz // the side away from the friend
	}
	in := physInput{DirX: sx, DirZ: sz}
	if !standable(b.c.instance.World.GetBlock, feetCell(p.X+sx, p.Y, p.Z+sz)) {
		in.DirX, in.DirZ = -sx, -sz // a ledge there: the other way
		if !standable(b.c.instance.World.GetBlock, feetCell(p.X-sx, p.Y, p.Z-sz)) {
			return physInput{} // nowhere to go: wait for them to move
		}
	}
	return in
}

// canShoot: a bow and arrows to shoot it with.
func (b *bot) canShoot() bool {
	return b.c.countItemName(itemBow) > 0 && b.c.countItemName(itemArrow) > 0
}

// sees reports a clear straight line from the bot's eyes to t's chest.
func (b *bot) sees(t *ClientConnection) bool {
	ts := t.player.Snapshot()
	chest := anticheat.Vec{X: ts.X, Y: ts.Y + 1.0, Z: ts.Z}
	_, blocked := firstBlockOnRay(b.c.instance.World.GetBlock, b.c.eye(), chest, world.Position{Y: math.MinInt32})
	return !blocked
}

// targetVelocity is t's horizontal speed (blocks/tick) since the bot last
// looked, for leading shots.
func (b *bot) targetVelocity(now uint64, t *ClientConnection) anticheat.Vec {
	br := &b.brain
	ts := t.player.Snapshot()
	pos := anticheat.Vec{X: ts.X, Y: ts.Y, Z: ts.Z}
	var v anticheat.Vec
	if br.leadOf == t && now > br.leadAt && now-br.leadAt <= 10 {
		dt := float64(now - br.leadAt)
		v = anticheat.Vec{X: (pos.X - br.leadPos.X) / dt, Z: (pos.Z - br.leadPos.Z) / dt}
	}
	br.leadOf, br.leadAt, br.leadPos = t, now, pos
	return v
}

// stopDraw lowers a bow being drawn.
func (b *bot) stopDraw() {
	if b.brain.drawAt != 0 {
		b.c.cancelUse()
		b.brain.drawAt = 0
	}
}

// aimArrow finds the yaw and pitch that send a full-draw arrow from from to
// to, simulating its flight (drag, then gravity, then the move, as
// stepProjectiles does) — the flattest arc that gets within half a block —
// and how many ticks the arrow takes.
func aimArrow(from, to anticheat.Vec) (yaw, pitch float32, flight int, ok bool) {
	dx, dz := to.X-from.X, to.Z-from.Z
	dist, dy := math.Hypot(dx, dz), to.Y-from.Y
	yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	best, bestScore, bestTicks := 0.0, math.Inf(1), 0
	for p := -45.0; p <= 45; p += 0.25 { // pitch: negative is up
		rad := p * math.Pi / 180
		vh, vy := arrowSpeed*math.Cos(rad), -arrowSpeed*math.Sin(rad)
		x, y := 0.0, 0.0
		ticks := 0
		for i := 0; i < 200 && x < dist; i++ {
			ticks = i + 1
			vh *= arrowDrag
			vy = vy*arrowDrag - arrowGravity
			nx, ny := x+vh, y+vy
			if nx >= dist {
				y += (ny - y) * (dist - x) / (nx - x)
				x = dist
				break
			}
			x, y = nx, ny
		}
		if x < dist {
			continue
		}
		if score := math.Abs(y-dy) + 0.001*math.Abs(p); score < bestScore {
			best, bestScore, bestTicks = p, score, ticks
		}
	}
	return yaw, float32(best), bestTicks, bestScore < 0.5
}

// --- gear -----------------------------------------------------------------------

// wareKindFor is the ware that digs blocks wanting tool fastest.
func wareKindFor(tool world.ToolType) string {
	switch tool {
	case world.Pickaxe:
		return game.WarePickaxe
	case world.Axe:
		return game.WareAxe
	case world.Shears:
		return game.WareShears
	}
	return ""
}

// wishList is the gear the bot wants, by kind in its order of preference,
// each kind's unowned tiers best first. With a hard bed defence in the way
// the tool for it (or TNT) comes first; away from home only that and what
// diamonds / emeralds buy count.
func (b *bot) wishList(now uint64) [][]game.BotWare {
	c, br := b.c, &b.brain
	kind := func(k string) []game.BotWare {
		var out []game.BotWare
		for _, w := range br.plan.Wares {
			if w.Kind == k && !w.Owned && now >= br.badWare[w.Key] {
				out = append(out, w)
			}
		}
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // best tier first
			out[i], out[j] = out[j], out[i]
		}
		return out
	}
	var groups [][]game.BotWare
	add := func(g []game.BotWare) {
		if len(g) > 0 {
			groups = append(groups, g)
		}
	}
	if br.role == rolePusher && c.countItemName(itemTNT) < botPusherTNT {
		add(kind(game.WareTNT)) // the pusher's tool, before anything else
	}
	if br.hardCover != world.NoTool {
		tool := kind(wareKindFor(br.hardCover))
		var tnt []game.BotWare
		if c.countItemName(itemTNT) == 0 {
			tnt = kind(game.WareTNT)
		}
		if b.style.prefersTNT {
			add(tnt)
			add(tool)
		} else {
			add(tool)
			add(tnt)
		}
	}
	far := horiz(b.phys.Pos, cellCentre(br.plan.Home)) > botHomeRange
	if far {
		// Away from home only what diamonds / emeralds buy (the diamond
		// sword and armour, team upgrades) is worth the trip back.
		add = func(g []game.BotWare) {
			var keep []game.BotWare
			for _, w := range g {
				if b.precious(w.Currency) {
					keep = append(keep, w)
				}
			}
			if len(keep) > 0 {
				groups = append(groups, keep)
			}
		}
	}
	if br.role == roleArcher {
		add(kind(game.WareBow))
		if c.countItemName(itemBow) > 0 && c.countItemName(itemArrow) < botArrowStock {
			add(kind(game.WareArrows))
		}
	}
	if b.style.armorFirst {
		add(kind(game.WareArmor))
		add(kind(game.WareSword))
	} else {
		add(kind(game.WareSword))
		add(kind(game.WareArmor))
	}
	// Team upgrades, each the next tier of one, in the game's order.
	for _, w := range br.plan.Wares {
		if w.Kind == game.WareUpgrade && !w.Owned && now >= br.badWare[w.Key] {
			add([]game.BotWare{w})
		}
	}
	if b.style.likesBow && br.role != roleArcher {
		add(kind(game.WareBow))
	}
	if br.role != roleArcher && c.countItemName(itemBow) > 0 && c.countItemName(itemArrow) < botArrowStock {
		add(kind(game.WareArrows))
	}
	return groups
}

// affords: the bot can pay for w and still keep one pack of blocks' worth
// when it's short of blocks.
func (b *bot) affords(w game.BotWare, blocks int) bool {
	plan := b.brain.plan
	reserve := 0
	if w.Currency == plan.Currency && blocks < botMinBlocks {
		reserve = plan.Price
	}
	return b.c.countItemName(w.Currency)-reserve >= w.Price
}

// wantWare is the gear to buy now: the first it wants that it can pay for.
func (b *bot) wantWare(blocks int) *game.BotWare {
	br := &b.brain
	if !br.plan.HasShop {
		return nil
	}
	// At an enemy bed nothing is worth the trip home — except the tool (or
	// TNT) for a cover it's stuck at (obsidian all round).
	base := b.onEnemyBase()
	if base && !br.hardStuck {
		return nil
	}
	now := b.c.instance.Tick()
	for _, g := range b.wishList(now) {
		if base && g[0].Kind != game.WareTNT && g[0].Kind != wareKindFor(br.hardCover) {
			continue
		}
		for _, w := range g {
			if b.affords(w, blocks) {
				return &w
			}
		}
	}
	return nil
}

// buy buys w, or a pack of blocks when w is nil.
func (b *bot) buy(w *game.BotWare) {
	c, br := b.c, &b.brain
	guide, ctx, me := c.instance.botGuide, c.instance.pluginCtx(), playerBridge{conn: c}
	if w == nil {
		guide.BotBuy(ctx, me, br.plan.BuyKey)
		return
	}
	if guide.BotBuy(ctx, me, w.Key) {
		switch {
		case w.Kind == game.WareTNT:
			br.hardCover, br.hardStuck = world.NoTool, false
		case w.Kind == wareKindFor(br.hardCover):
			// Done once the tools dig the cover in good time; else the next
			// tier (the plan offers it once refreshed).
			if _, t := b.bestTool(br.hardBlock); br.hardBlock == world.Air || (t >= 0 && t <= botMaxDig) {
				br.hardCover, br.hardStuck = world.NoTool, false
			}
		}
	} else {
		if br.badWare == nil {
			br.badWare = map[string]uint64{}
		}
		br.badWare[w.Key] = c.instance.Tick() + botWareRetry
	}
	br.planAt = 0 // what it owns changed: ask the game again
}

// saving reports whether the bot stays at the station to save up for gear
// it wants but can't afford — decided now and then, by chance.
func (b *bot) saving(now uint64) bool {
	br := &b.brain
	if !br.plan.HasStation || !br.plan.HasShop || horiz(b.phys.Pos, cellCentre(br.plan.Home)) > botHomeRange {
		return false
	}
	// A pusher without TNT waits for the gold (up to botPusherSave).
	if br.role == rolePusher && b.c.countItemName(itemTNT) == 0 && b.offers(game.WareTNT) {
		if br.pushSaveAt == 0 {
			br.pushSaveAt = now
		}
		if now < br.pushSaveAt+botPusherSave {
			return true
		}
	} else {
		br.pushSaveAt = 0
	}
	var wish [][]game.BotWare
	for _, g := range b.wishList(now) {
		if !b.precious(g[0].Currency) { // the station doesn't make those
			wish = append(wish, g)
		}
	}
	if now >= br.farmDecideAt {
		br.farmDecideAt = now + botFarmEvery
		if len(wish) > 0 && b.rng.Float64() < b.skill.farm {
			br.farmUntil = now + 200 + uint64(b.rng.Intn(400))
		}
	}
	return now < br.farmUntil && len(wish) > 0
}

// --- TNT ------------------------------------------------------------------------

// blast places a TNT next to cover, on the bot's side, and runs. False when
// the bot has none, the game doesn't light placed TNT, or there's no spot.
func (b *bot) blast(now uint64, cover world.Position) bool {
	c, br := b.c, &b.brain
	if c.countItemName(itemTNT) == 0 || !c.instance.TNTAutoPrime() {
		return false
	}
	at := c.instance.World.GetBlock
	eye := c.eye()
	var spot world.Position
	bestD := math.Inf(1)
	for _, d := range [6][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
		p := cover.Add(d[0], d[1], d[2])
		if !world.Replaceable(at(p)) || c.instance.blockedByPlayer(p) {
			continue
		}
		if dd := anticheat.Dist(eye, blockCentre(p)); dd < bestD {
			spot, bestD = p, dd
		}
	}
	if bestD > botDigReach+0.5 {
		return false
	}
	b.lookAt(blockCentre(spot))
	if !c.botPlaceWith(spot, func(n string) bool { return n == itemTNT }) {
		return false
	}
	br.dig, br.route = nil, nil
	br.fleeFrom, br.fleeUntil = spot, now+botTNTFlee
	return true
}

// flee walks away from the TNT it lit, homeward.
func (b *bot) flee(now uint64) physInput {
	from := b.brain.fleeFrom
	away := func(p world.Position) bool {
		dx, dy, dz := float64(p.X-from.X), float64(p.Y-from.Y), float64(p.Z-from.Z)
		return math.Sqrt(dx*dx+dy*dy+dz*dz) >= botFleeDist
	}
	return b.goTo(now, fmt.Sprintf("flee:%v", from), b.brain.plan.Home, away, 0)
}

// --- diamonds and emeralds ------------------------------------------------------

const (
	botPickupRadius = 2.0  // drops this close to a pickup spot are its pile
	botRaidRest     = 1200 // after a raid, at least this long (and up to
	// twice that) for other things — attacking — before the next
)

// precious reports whether item is what a pickup spot drops (diamonds,
// emeralds) rather than what the bot's own station makes.
func (b *bot) precious(item string) bool {
	for _, p := range b.brain.plan.Pickups {
		if p.Item == item {
			return true
		}
	}
	return false
}

// offers reports whether the shop sells a ware of this kind.
func (b *bot) offers(kind string) bool {
	for _, w := range b.brain.plan.Wares {
		if w.Kind == kind {
			return true
		}
	}
	return false
}

// wantsCurrency reports whether some ware the bot doesn't own is paid in item.
func (b *bot) wantsCurrency(item string) bool {
	for _, w := range b.brain.plan.Wares {
		if w.Currency == item && !w.Owned {
			return true
		}
	}
	return false
}

// pilePos is where a pickup spot's drops lie.
func pilePos(p world.Position) anticheat.Vec {
	return anticheat.Vec{X: float64(p.X) + 0.5, Y: float64(p.Y), Z: float64(p.Z) + 0.5}
}

// collect goes for the best pile of diamonds / emeralds worth having —
// something it wants is paid in them — and stands at it while it picks the
// pile up. Greedy bots go for any pile, the others for a big one. Once the
// bot can afford something, the shop (wantWare) sends it home. Between
// raids it rests (endRaid), so it still goes for the beds.
func (b *bot) collect(now uint64, blocks int) (physInput, bool) {
	c, br := b.c, &b.brain
	plan := br.plan
	if br.role == roleDefender || len(plan.Pickups) == 0 || (!br.raiding && now < br.nextRaid) {
		return physInput{}, false
	}
	need := 2
	if b.style.greedy {
		need = 1
	}
	var best *game.BotPickup
	bestScore := 0.0
	for i := range plan.Pickups {
		p := &plan.Pickups[i]
		if until, ok := br.failed[p.Pos]; (ok && now < until) || !b.wantsCurrency(p.Item) {
			continue
		}
		at := pilePos(p.Pos)
		n := c.instance.DroppedItemsNear(at.X, at.Y, at.Z, botPickupRadius, p.Item)
		if n == 0 || (n < need && !(br.raiding && br.pickup == p.Pos)) {
			continue
		}
		// Emeralds buy the best gear; nearer piles first.
		weight := 2.0
		if p.Item == "minecraft:emerald" {
			weight = 3
		}
		score := weight*float64(n) - anticheat.Dist(c.feet(), at)/16
		if best == nil || score > bestScore {
			best, bestScore = p, score
		}
	}
	if best == nil {
		b.endRaid(now)
		return physInput{}, false
	}
	br.pickup, br.raiding = best.Pos, true
	goal := atStation(best.Pos)
	if goal(c.feetCellNow()) {
		in := b.toward(pilePos(best.Pos), false) // into the pile
		in.DirX, in.DirZ = in.DirX*0.3, in.DirZ*0.3
		return in, true
	}
	in, status := b.goToFar(now, fmt.Sprintf("pickup:%v", best.Pos), best.Pos, goal, blocks)
	if status == farFailed {
		b.giveUp(now, best.Pos)
		b.endRaid(now)
		return physInput{}, false
	}
	return in, true
}

// endRaid closes a raid (the pile is taken, or out of reach): the next one
// waits botRaidRest..2×botRaidRest ticks.
func (b *bot) endRaid(now uint64) {
	br := &b.brain
	if br.raiding {
		br.raiding = false
		br.nextRaid = now + botRaidRest + uint64(b.rng.Intn(botRaidRest))
	}
}
