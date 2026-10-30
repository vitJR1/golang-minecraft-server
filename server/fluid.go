package server

import (
	"bytes"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/combat"
	"minecraft-server/server/fluid"
	"minecraft-server/server/sound"
	"minecraft-server/world"
)

// fluid.go: fluids in an instance — the flow model is server/fluid (Of,
// Update, Queue) — and what they do to players: lava burns, fire, water
// puts it out.

const (
	lavaDamage        = 4
	lavaDamageTicks   = 10 // vanilla: 4 damage every half second while inside
	lavaCheckInterval = 10
)

// scheduleFluid marks pos and its six neighbours for fluid re-evaluation.
func (i *Instance) scheduleFluid(pos world.Position) { i.fluids.Schedule(pos) }

// fluidTick re-evaluates every pending fluid block whose kind is due this
// tick.
func (i *Instance) fluidTick(tick uint64) {
	for _, pos := range i.fluids.Due(tick, i.World.GetBlock) {
		fluid.Update(fluidBlocks{i}, pos)
	}
}

// fluidBlocks is an instance as the world fluids flow in.
type fluidBlocks struct{ i *Instance }

func (f fluidBlocks) GetBlock(pos world.Position) world.Block    { return f.i.World.GetBlock(pos) }
func (f fluidBlocks) SetBlock(pos world.Position, b world.Block) { f.i.SetBlock(pos, b) }

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
		inLava := feetBlk == world.Lava.Name || headBlk == world.Lava.Name
		inWater := feetBlk == world.Water.Name || headBlk == world.Water.Name

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

// broadcastOnFire refreshes the entity flags (fire bit 0x01 together with
// the other flag bits, see entityFlags) for everyone in the instance.
func (c *ClientConnection) broadcastOnFire(bool) { c.broadcastEntityFlags() }

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
	applied, newHealth, killed := p.ApplyDamage(c.absorbDamage(amount), now, invulnTicks)
	if applied <= 0 {
		return
	}
	s := p.Snapshot()
	c.instance.Players.Broadcast(packetid.CbPlayHurtAnimation, combat.HurtAnimationPayload(s.EntityID, 0), -1)
	c.instance.playSound("minecraft:entity.player.hurt", sound.CategoryPlayer, s.X, s.Y, s.Z, 1, 1)
	if killed {
		c.die(nil)
		return
	}
	_ = c.sendSetHealth(newHealth)
}
