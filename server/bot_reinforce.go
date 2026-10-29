package server

import (
	"minecraft-server/world"
)

// bot_reinforce.go: bots learn where they fall. The last route cell a bot
// stood on before it dropped off the route — into the void, or two blocks
// or more below where the route went — is a fall spot; the falls are
// counted per team (teamBrain.falls; a bot without a team keeps its own).
// After botFallsToReinforce falls at a spot, any bot of the team passing
// it widens the way there: a block at each side of the walkway, under the
// cells beside it, so the narrow bridge it kept falling off becomes three
// wide.

const (
	botFallsToReinforce = 2   // falls at one spot before it's widened
	botFallDepth        = 2.5 // below the last floor this far, off-route: a fall
	botFallMemory       = 80  // ticks after leaving the floor a fall still counts for it
	botWidenAhead       = 2   // cells past the fall spot widened too
)

// fallSpots is the falls map the bot records to and reads from.
func (b *bot) fallSpots() map[world.Position]int {
	if tb := b.teamBrainFor(); tb != nil {
		if tb.falls == nil {
			tb.falls = map[world.Position]int{}
		}
		return tb.falls
	}
	if b.brain.falls == nil {
		b.brain.falls = map[world.Position]int{}
	}
	return b.brain.falls
}

// noteFloor remembers the route cell the bot stands on as where a fall
// would have started.
func (b *bot) noteFloor(now uint64, here world.Position) {
	br := &b.brain
	br.lastFloor, br.lastFloorAt, br.fallNoted = here, now, false
}

// watchFall counts a fall when the bot is well below its last route floor
// without the route going down there. Runs after the tick's physics.
func (b *bot) watchFall(now uint64) {
	br := &b.brain
	if b.phys.OnGround || br.fallNoted || br.lastFloorAt == 0 || now > br.lastFloorAt+botFallMemory {
		return
	}
	if b.phys.Pos.Y > float64(br.lastFloor.Y)-botFallDepth {
		return
	}
	// A drop the route itself takes isn't a fall.
	if br.route != nil && br.routeIdx < len(br.route) && br.route[br.routeIdx].Cell.Y < br.lastFloor.Y-1 {
		return
	}
	br.fallNoted = true
	b.fallSpots()[br.lastFloor]++
}

// reinforce widens the way around a fall spot the bot is passing: from where
// it stands, a block beside the walkway under each side cell of here and of
// the next step. True while it's placing.
func (b *bot) reinforce(now uint64, here, next world.Position) (physInput, bool) {
	c, br := b.c, &b.brain
	spots := b.fallSpots()
	if len(spots) == 0 || !b.phys.OnGround || c.countItemName(br.plan.BlockItem) == 0 {
		return physInput{}, false
	}
	var spot world.Position
	found := false
	for p, n := range spots {
		if n >= botFallsToReinforce && p.Y == here.Y && abs(p.X-here.X) <= 1 && abs(p.Z-here.Z) <= 1 {
			spot, found = p, true
			break
		}
	}
	if !found {
		return physInput{}, false
	}
	at := c.instance.World.GetBlock
	dx, dz := sign(next.X-here.X), sign(next.Z-here.Z)
	// The spot is the last cell it stood on; the fall itself came a step
	// or two further: widen from one before the spot to two past it.
	cells := []world.Position{here, next}
	for k := -1; k <= botWidenAhead; k++ {
		cells = append(cells, spot.Add(k*dx, 0, k*dz))
	}
	var sides []world.Position
	for _, cell := range cells {
		floor := cell.Add(0, -1, 0)
		if dx != 0 || (dx == 0 && dz == 0) {
			sides = append(sides, floor.Add(0, 0, 1), floor.Add(0, 0, -1))
		}
		if dz != 0 || (dx == 0 && dz == 0) {
			sides = append(sides, floor.Add(1, 0, 0), floor.Add(-1, 0, 0))
		}
	}
	for _, s := range sides {
		if !world.Replaceable(at(s)) || c.instance.blockedByPlayer(s) {
			continue
		}
		b.lookAt(blockCentre(s))
		if now >= br.nextPlace {
			br.nextPlace = now + b.skill.placeGap
			if !c.botPlace(s, br.plan.BlockItem) {
				continue
			}
		}
		return physInput{}, true
	}
	delete(spots, spot) // widened: done here
	return physInput{}, false
}
