package server

import (
	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/gameplay/combat"
	"minecraft-server/server/gameplay/fluid"
	"minecraft-server/server/gameplay/sound"
	"minecraft-server/world"
)

// fluid.go: fluids in an instance — the flow model is server/gameplay/fluid (Of,
// Update, Queue) — and what they do to players: lava burns, fire, water
// puts it out.

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

// burnTick runs every tick: players standing in lava take lava damage and
// catch fire; burning players keep taking fire damage after they climb out
// until the fire runs down or they touch water (the rules: fluid.BurnStep). Gated on the instance's
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
		b := fluid.BurnStep(i.World.GetBlock(feet), i.World.GetBlock(head), c.fireTicks.Load(), tick)
		// Death puts the fire out (die); an instant respawn must not have
		// it relit from the spot the player burned on.
		if b.Damage > 0 && c.hurtEnvironment(b.Damage, b.Invuln, tick) {
			continue
		}
		switch {
		case b.Ignite:
			c.setFire(b.Fire)
		case b.Fire <= 0:
			c.extinguish()
		default:
			c.fireTicks.Store(b.Fire)
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

// hurtEnvironment applies non-player damage with its own invulnerability
// window: red flash + hurt sound for everyone, then health sync or death.
// Reports whether the damage killed the player.
func (c *ClientConnection) hurtEnvironment(amount float32, invulnTicks, now uint64) (killed bool) {
	p := c.player
	if p == nil || c.instance == nil {
		return false
	}
	dmg, wear := c.absorbDamage(amount)
	applied, newHealth, killed := p.ApplyDamage(dmg, now, invulnTicks)
	if applied <= 0 {
		return false
	}
	c.wearArmor(wear)
	s := p.Snapshot()
	c.instance.Players.Broadcast(packetid.CbPlayHurtAnimation, combat.HurtAnimationPayload(s.EntityID, 0), -1)
	c.instance.playSound("minecraft:entity.player.hurt", sound.CategoryPlayer, s.X, s.Y, s.Z, 1, 1)
	if killed {
		c.die(nil)
		return true
	}
	_ = c.sendSetHealth(newHealth)
	return false
}
