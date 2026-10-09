package server

import (
	"bytes"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// fluid.go: vanilla-style fluid flow and lava damage, driven from the
// instance tick loop.
//
// Fluids are ordinary blocks whose state encodes the "level" property:
// 0 = source, 1..7 = flowing (1 right next to the source, thinner each
// step), 8..15 = falling (a full column below another fluid block). Water
// updates every 5 ticks and thins by 1 per block (reach 7); lava every 30
// ticks, thinning by 2 (reach 3, like the overworld).
//
// Nothing flows on its own at map load: a fluid block is re-evaluated only
// when it or one of its six neighbours changes (Instance.SetBlock /
// SetBlocks schedule it). Each evaluation does what vanilla's
// FlowingFluid.tick does — derive the block's correct level from its
// neighbours (draining when the source is gone, turning into a new source
// between two sources), then spread: down into air as falling fluid, and
// sideways (sources always; flowing fluid only when it can't fall) into air
// or thinner fluid of the same kind. Water and lava don't interact.

// fluidKind holds one fluid's tuning.
type fluidKind struct {
	name     string
	minState int32 // state id of level 0
	interval uint64
	dropOff  int
}

var (
	fluidWater = &fluidKind{name: world.Water.Name, minState: world.Water.StateID, interval: 5, dropOff: 1}
	fluidLava  = &fluidKind{name: world.Lava.Name, minState: world.Lava.StateID, interval: 30, dropOff: 2}
	fluidKinds = []*fluidKind{fluidWater, fluidLava}
)

const (
	fluidMaxLevel     = 7 // thinnest flowing level
	fluidFalling      = 8 // level used for a falling column
	lavaDamage        = 4
	lavaDamageTicks   = 10 // vanilla: 4 damage every half second while inside
	lavaCheckInterval = 10
)

// fluidOf identifies a fluid block: its kind and level.
func fluidOf(b world.Block) (*fluidKind, int, bool) {
	for _, k := range fluidKinds {
		if b.Name == k.name {
			lvl := int(b.StateID - k.minState)
			if lvl < 0 || lvl > 15 {
				lvl = 0
			}
			return k, lvl, true
		}
	}
	return nil, 0, false
}

func (k *fluidKind) block(level int) world.Block {
	return world.Block{StateID: k.minState + int32(level), Name: k.name}
}

// spreadLevel is the level a block contributes to its side neighbours:
// sources and falling columns count as full.
func spreadLevel(level int) int {
	if level >= fluidFalling {
		return 0
	}
	return level
}

// scheduleFluid marks pos and its six neighbours for fluid re-evaluation.
func (i *Instance) scheduleFluid(pos world.Position) {
	i.fluidMu.Lock()
	if i.fluidPending == nil {
		i.fluidPending = map[world.Position]struct{}{}
	}
	i.fluidPending[pos] = struct{}{}
	for _, n := range neighbours6(pos) {
		i.fluidPending[n] = struct{}{}
	}
	i.fluidMu.Unlock()
}

func neighbours6(p world.Position) [6]world.Position {
	return [6]world.Position{
		{X: p.X, Y: p.Y - 1, Z: p.Z}, {X: p.X, Y: p.Y + 1, Z: p.Z},
		{X: p.X - 1, Y: p.Y, Z: p.Z}, {X: p.X + 1, Y: p.Y, Z: p.Z},
		{X: p.X, Y: p.Y, Z: p.Z - 1}, {X: p.X, Y: p.Y, Z: p.Z + 1},
	}
}

func sides4(p world.Position) [4]world.Position {
	return [4]world.Position{
		{X: p.X - 1, Y: p.Y, Z: p.Z}, {X: p.X + 1, Y: p.Y, Z: p.Z},
		{X: p.X, Y: p.Y, Z: p.Z - 1}, {X: p.X, Y: p.Y, Z: p.Z + 1},
	}
}

// fluidTick re-evaluates every pending fluid block whose kind is due this
// tick. Pending non-fluid positions are dropped (they were scheduled as
// neighbours of a change and turned out not to hold fluid).
func (i *Instance) fluidTick(tick uint64) {
	i.fluidMu.Lock()
	if len(i.fluidPending) == 0 {
		i.fluidMu.Unlock()
		return
	}
	due := make([]world.Position, 0, len(i.fluidPending))
	for pos := range i.fluidPending {
		k, _, ok := fluidOf(i.World.GetBlock(pos))
		switch {
		case !ok:
			delete(i.fluidPending, pos)
		case tick%k.interval == 0:
			due = append(due, pos)
			delete(i.fluidPending, pos)
		}
	}
	i.fluidMu.Unlock()
	for _, pos := range due {
		i.updateFluid(pos)
	}
}

// updateFluid is one vanilla fluid tick for the block at pos.
func (i *Instance) updateFluid(pos world.Position) {
	cur := i.World.GetBlock(pos)
	k, level, ok := fluidOf(cur)
	if !ok {
		return
	}
	below := world.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	belowBlk := i.World.GetBlock(below)
	_, belowLevel, belowIsFluid := fluidOf(belowBlk)
	belowSame := belowIsFluid && belowBlk.Name == k.name

	// 1. Derive the level this block should have from its neighbours.
	if level != 0 {
		want := -1
		aboveBlk := i.World.GetBlock(world.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z})
		if aboveBlk.Name == k.name {
			want = fluidFalling
		} else {
			sources := 0
			for _, n := range sides4(pos) {
				nb := i.World.GetBlock(n)
				if nb.Name != k.name {
					continue
				}
				_, nl, _ := fluidOf(nb)
				if nl == 0 {
					sources++
				}
				if l := spreadLevel(nl) + k.dropOff; l <= fluidMaxLevel && (want < 0 || l < want) {
					want = l
				}
			}
			// Infinite water: two adjacent sources over solid ground (or a
			// source) make a new source. Lava never does.
			if k == fluidWater && sources >= 2 && (belowSame && belowLevel == 0 || (!belowIsFluid && belowBlk != world.Air)) {
				want = 0
			}
		}
		if want < 0 {
			i.SetBlock(pos, world.Air) // nothing feeds it any more
			return
		}
		if want != level {
			i.SetBlock(pos, k.block(want))
			level = want
		}
	}

	// 2. Spread. Down first.
	if belowBlk == world.Air || (belowSame && belowLevel != 0 && belowLevel != fluidFalling) {
		i.SetBlock(below, k.block(fluidFalling))
	}
	// Sideways: sources always; flowing fluid only when it can't fall.
	hole := belowBlk == world.Air || belowSame
	if level != 0 && hole {
		return
	}
	next := spreadLevel(level) + k.dropOff
	if next > fluidMaxLevel {
		return
	}
	for _, n := range sides4(pos) {
		nb := i.World.GetBlock(n)
		if nb == world.Air {
			i.SetBlock(n, k.block(next))
			continue
		}
		if nb.Name == k.name {
			if _, nl, _ := fluidOf(nb); nl != 0 && nl < fluidFalling && nl > next {
				i.SetBlock(n, k.block(next))
			}
		}
	}
}

// Fire tuning (vanilla): lava sets a player on fire for 15 seconds; fire
// deals 1 damage every second; water puts it out.
const (
	fireFromLavaTicks = 300
	fireDamage        = 1
	fireDamageEvery   = 20
	fireInvulnTicks   = 10
)

// burnTick runs every tick: players standing in lava take lava damage and
// catch fire; burning players keep taking fire damage after they climb out
// until the fire runs down or they touch water. Gated on the instance's
// combat switch so lobbies stay safe. Creative/spectator players neither
// burn nor take damage.
func (i *Instance) burnTick(tick uint64) {
	if !i.PvPEnabled() {
		return
	}
	for _, c := range i.Players.snapshot() {
		p := c.player
		if p == nil {
			continue
		}
		s := p.Snapshot()
		if p.IsDead() || s.Gamemode == player.Creative || s.Gamemode == player.Spectator {
			c.extinguish()
			continue
		}
		feet := world.Position{X: floorF(s.X), Y: floorF(s.Y), Z: floorF(s.Z)}
		head := world.Position{X: feet.X, Y: floorF(s.Y + eyeHeight), Z: feet.Z}
		feetBlk, headBlk := i.World.GetBlock(feet).Name, i.World.GetBlock(head).Name
		inLava := feetBlk == fluidLava.name || headBlk == fluidLava.name
		inWater := feetBlk == fluidWater.name || headBlk == fluidWater.name

		switch {
		case inLava:
			if tick%lavaCheckInterval == 0 {
				c.hurtEnvironment(lavaDamage, lavaDamageTicks, tick)
			}
			c.setFire(fireFromLavaTicks)
			continue
		case inWater:
			c.extinguish()
			continue
		}
		left := c.fireTicks.Load()
		if left <= 0 {
			continue
		}
		if left%fireDamageEvery == 0 {
			c.hurtEnvironment(fireDamage, fireInvulnTicks, tick)
		}
		if left-1 <= 0 {
			c.extinguish()
		} else {
			c.fireTicks.Store(left - 1)
		}
	}
}

// lavaTick is kept as the lava-only entry point used by tests; burnTick is
// what the tick loop runs.
func (i *Instance) lavaTick(tick uint64) { i.burnTick(tick) }

// setFire (re)starts the player's fire for ticks, showing the flames to
// everyone when they weren't burning before.
func (c *ClientConnection) setFire(ticks int32) {
	was := c.fireTicks.Swap(ticks)
	if was <= 0 {
		c.broadcastOnFire(true)
	}
}

// extinguish puts the player's fire out (no-op when not burning).
func (c *ClientConnection) extinguish() {
	if c.fireTicks.Swap(0) > 0 {
		c.broadcastOnFire(false)
	}
}

// broadcastOnFire flips the "on fire" entity flag (metadata index 0, bit
// 0x01) for everyone in the instance, including the player's own client,
// which renders the first-person flame overlay from it.
func (c *ClientConnection) broadcastOnFire(on bool) {
	if c.player == nil || c.instance == nil {
		return
	}
	var flags byte
	if on {
		flags |= 0x01
	}
	c.instance.Players.Broadcast(CbPlaySetEntityMetadata, entityFlagsPayload(c.player.EntityID, flags), -1)
}

// entityFlagsPayload builds Set Entity Metadata with just the index-0 byte
// (on fire / sneaking / sprinting / … bitmask).
func entityFlagsPayload(eid int32, flags byte) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, eid)
	buf.WriteByte(0)                        // index 0: entity flags
	protocol.WriteVarInt32ToBuffer(&buf, 0) // type: Byte
	buf.WriteByte(flags)
	buf.WriteByte(0xFF)
	return buf.Bytes()
}

// hurtEnvironment applies non-player damage with its own invulnerability
// window: red flash + hurt sound for everyone, then health sync or death.
func (c *ClientConnection) hurtEnvironment(amount float32, invulnTicks, now uint64) {
	p := c.player
	if p == nil || c.instance == nil {
		return
	}
	applied, newHealth, killed := p.ApplyDamage(amount, now, invulnTicks)
	if applied <= 0 {
		return
	}
	s := p.Snapshot()
	c.instance.Players.Broadcast(CbPlayHurtAnimation, hurtAnimationPayload(s.EntityID, 0), -1)
	c.instance.playSound("minecraft:entity.player.hurt", soundCategoryPlayer, s.X, s.Y, s.Z, 1, 1)
	if killed {
		c.die(nil)
		return
	}
	_ = c.sendSetHealth(newHealth)
}
