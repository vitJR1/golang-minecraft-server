package server

import (
	"minecraft-server/anticheat"
	"minecraft-server/world"
)

// bot_pusher.go: the pusher, one per team (the team brain's lead). Its job
// is the enemy beds, whatever it takes: it buys TNT first (and waits at the
// forge for the gold), doesn't stop for fights unless an enemy is on top of
// it, never falls back, doesn't raid generators. At an enemy bed covered on
// top it towers up beside it (jump, block under the feet) until it stands
// above the cover, drops a TNT on top, runs, and then digs the bed out.
// Once no enemy bed is left it fights like everyone else.

const (
	botPusherTNT     = 2   // TNT a pusher carries before setting out
	botPusherSave    = 600 // longest it waits at the forge for its first TNT
	botPushGap       = 120 // ticks between TNT drops on one bed (the fuse is 50)
	botTowerMax      = 4   // highest it towers
	botPusherEngage  = 4.0 // enemies closer than this are fought
	botTowerClearing = 1.05
)

// pushTNT drops a TNT on top of the cover over bed, towering up first when
// the cover is above it. False when there's nothing for TNT to do (the bed
// is open on top, no TNT, the game doesn't light it, a ceiling) — then the
// bed is dug as usual.
func (b *bot) pushTNT(now uint64, bed world.Position) (physInput, bool) {
	c, br := b.c, &b.brain
	if c.countItemName(itemTNT) == 0 || !c.instance.TNTAutoPrime() || now < br.nextPush {
		return physInput{}, false
	}
	at := c.instance.World.GetBlock
	spot, ok := dropSpot(at, bed)
	if !ok || !tntBreaks(at(spot.Add(0, -1, 0))) {
		// Open on top — or the cover is blast-proof (obsidian): no TNT.
		br.towerBase = 0
		return physInput{}, false
	}
	// Stand above the cover: tower up to the spot's level.
	if b.phys.Pos.Y < float64(spot.Y)-0.01 || !b.phys.OnGround {
		if in, ok := b.tower(now, spot.Y); ok {
			return in, true
		}
	}
	if anticheat.Dist(c.eye(), blockCentre(spot)) > botDigReach+0.5 {
		return physInput{}, false
	}
	b.lookAt(blockCentre(spot))
	if !c.botPlaceWith(spot, func(n string) bool { return n == itemTNT }) {
		br.nextPush = now + botPushGap
		return physInput{}, false
	}
	br.nextPush = now + botPushGap
	br.dig, br.route, br.towering, br.towerBase = nil, nil, false, 0
	br.fleeFrom, br.fleeUntil = spot, now+botTNTFlee
	return physInput{}, true
}

// dropSpot is the first free cell above bed's column when something covers
// it on top (up to five blocks up); false when the top is open — nothing
// for TNT to clear there.
func dropSpot(at blockAt, bed world.Position) (world.Position, bool) {
	for y := 1; y <= 5; y++ {
		p := bed.Add(0, y, 0)
		if world.Replaceable(at(p)) {
			return p, y > 1
		}
	}
	return world.Position{}, false
}

// tower pillars up in place toward feet level top: jump, and once the feet
// are clear of the cell they left, fill it with a block to land on. False
// when it can't (out of blocks, a ceiling, already high enough on the
// ground, too high).
func (b *bot) tower(now uint64, top int) (physInput, bool) {
	c, br := b.c, &b.brain
	at := c.instance.World.GetBlock
	if b.phys.OnGround {
		here := c.feetCellNow()
		br.towering = false
		if here.Y >= top || here.Y-br.towerBase >= botTowerMax && br.towerBase != 0 ||
			c.countItemName(br.plan.BlockItem) == 0 || !headClear(at, here.Add(0, 2, 0)) {
			return physInput{}, false
		}
		if br.towerBase == 0 || here.Y < br.towerBase {
			br.towerBase = here.Y
		}
		br.towerCell, br.towering = here, true
		b.brain.pitch = 90 // looking down at the feet
		return physInput{Jump: true}, true
	}
	if !br.towering {
		return physInput{}, false // falling for some other reason
	}
	cell := br.towerCell
	if b.phys.Pos.Y >= float64(cell.Y)+botTowerClearing && world.Replaceable(at(cell)) && now >= br.nextPlace {
		b.lookAt(blockCentre(cell))
		if c.botPlace(cell, br.plan.BlockItem) {
			br.nextPlace = now + 1
		}
	}
	// Stay over the column.
	in := b.toward(cellCentre(cell), false)
	in.DirX, in.DirZ = in.DirX*0.2, in.DirZ*0.2
	return in, true
}
