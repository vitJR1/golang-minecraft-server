package server

import (
	"fmt"
	"math"

	"minecraft-server/anticheat"
	"minecraft-server/game"
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
	botBowRange   = 40.0 // farthest shot
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
	noWall := world.Position{Y: math.MinInt32}
	if anticheat.Dist(from, aim) > botBowRange {
		b.stopDraw()
		return physInput{}, false
	}
	if _, blocked := firstBlockOnRay(c.instance.World.GetBlock, c.eye(), aim, noWall); blocked {
		b.stopDraw()
		return physInput{}, false
	}
	yaw, pitch, ok := aimArrow(from, aim)
	if !ok {
		b.stopDraw()
		return physInput{}, false
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

// stopDraw lowers a bow being drawn.
func (b *bot) stopDraw() {
	if b.brain.drawAt != 0 {
		b.c.cancelUse()
		b.brain.drawAt = 0
	}
}

// aimArrow finds the yaw and pitch that send a full-draw arrow from from to
// to, simulating its flight (drag, then gravity, then the move, as
// stepProjectiles does) — the flattest arc that gets within half a block.
func aimArrow(from, to anticheat.Vec) (yaw, pitch float32, ok bool) {
	dx, dz := to.X-from.X, to.Z-from.Z
	dist, dy := math.Hypot(dx, dz), to.Y-from.Y
	yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	best, bestScore := 0.0, math.Inf(1)
	for p := -45.0; p <= 45; p += 0.25 { // pitch: negative is up
		rad := p * math.Pi / 180
		vh, vy := arrowSpeed*math.Cos(rad), -arrowSpeed*math.Sin(rad)
		x, y := 0.0, 0.0
		for i := 0; i < 200 && x < dist; i++ {
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
			best, bestScore = p, score
		}
	}
	return yaw, float32(best), bestScore < 0.5
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
// the tool for it (or TNT) comes first; away from home only that counts.
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
	if horiz(b.phys.Pos, cellCentre(br.plan.Home)) > botHomeRange {
		return groups
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
	if !b.brain.plan.HasShop {
		return nil
	}
	now := b.c.instance.Tick()
	for _, g := range b.wishList(now) {
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
		if w.Kind == game.WareTNT || w.Kind == wareKindFor(br.hardCover) {
			br.hardCover = world.NoTool
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
	wish := b.wishList(now)
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
