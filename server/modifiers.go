package server

import (
	"bytes"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/attributes"
	"minecraft-server/server/effect"
	"minecraft-server/server/enchant"
	"minecraft-server/server/inventory"
	"minecraft-server/server/sound"
	"minecraft-server/world"
)

// modifiers.go: the pluggable sources of dig / durability / combat
// modifiers — enchantments (server/enchant: Server.Enchantments, default
// enchant.NBT) and potion effects (server/effect: Server.Effects, default
// effectTable, timed effects per connection granted by /effect and games and
// shown through Entity Effect packets) — and what effects do to a player.

// enchantOf returns the level of enchantment on st via the server's
// Enchantments source (0 without a server).
func (c *ClientConnection) enchantOf(st inventory.Stack, enchantment string) int {
	if c.server == nil || c.server.Enchantments == nil {
		return 0
	}
	return c.server.Enchantments.Level(st, enchantment)
}

// heldEnchant is enchantOf for the held hotbar stack.
func (c *ClientConnection) heldEnchant(enchantment string) int {
	return c.enchantOf(c.inv.Held(c.heldSlot.Load()), enchantment)
}

// Effects answers "what amplifier+1 of e does this player have now"
// (0 = none, 1 = level I, …).
type Effects interface {
	Level(c *ClientConnection, e effect.Effect) int
}

// effectTable is the default Effects: the per-connection timed effects
// (effect.Table) and what granting / ending them shows the player.
type effectTable struct {
	effect.Table[*ClientConnection]
}

func newEffectTable() *effectTable { return &effectTable{} }

// apply grants e at level (amplifier+1) for d, telling the client.
func (t *effectTable) apply(c *ClientConnection, e effect.Effect, level int, d time.Duration) {
	t.Apply(c, e, level, d)
	if c.player != nil {
		_ = c.safeWrite(packetid.CbPlayEntityEffect, effect.EntityPayload(c.player.EntityID, e, level, d))
	}
	if e == effect.Invisibility {
		c.broadcastEntityFlags()
	}
	if e == effect.Absorption {
		c.grantAbsorption(level)
	}
	if attributes.AffectsMovementSpeed(e) {
		_ = c.sendMovementAttributes() // the client won't speed up on the effect alone
	}
}

// sweep drops every expired effect, telling each client (and, for
// invisibility, everyone).
func (t *effectTable) sweep() {
	for c, list := range t.Sweep() {
		c.sendEffectsRemoved(list)
	}
}

// remove clears one effect (or all, when e is nil) and tells the client.
func (t *effectTable) remove(c *ClientConnection, e *effect.Effect) {
	c.sendEffectsRemoved(t.Remove(c, e))
}

// sendEffectsRemoved tells the client these effects ended and refreshes the
// entity flags if invisibility was among them.
func (c *ClientConnection) sendEffectsRemoved(list []effect.Effect) {
	if c.player == nil {
		return
	}
	invisible, movement, absorption := false, false, false
	for _, e := range list {
		_ = c.safeWrite(packetid.CbPlayRemoveEntityEffect, effect.RemovePayload(c.player.EntityID, e))
		if e == effect.Invisibility {
			invisible = true
		}
		if attributes.AffectsMovementSpeed(e) {
			movement = true
		}
		if e == effect.Absorption {
			absorption = true
		}
	}
	if invisible {
		c.broadcastEntityFlags()
	}
	if movement {
		_ = c.sendMovementAttributes()
	}
	if absorption {
		// The effect ending takes the leftover yellow hearts with it.
		c.player.SetAbsorption(0)
		c.syncAbsorption(false)
	}
}

// --- per-connection effect API (games bridge these) -------------------------

// applyEffect grants e at level (1 = I) for d. No-op without a server.
func (c *ClientConnection) applyEffect(e effect.Effect, level int, d time.Duration) {
	if c.server == nil || c.server.effects == nil || level < 1 {
		return
	}
	c.server.effects.apply(c, e, level, d)
}

// RemoveEffect ends e now (no-op when absent).
func (c *ClientConnection) RemoveEffect(e effect.Effect) {
	if c.server == nil || c.server.effects == nil {
		return
	}
	c.server.effects.remove(c, &e)
}

// clearEffects ends every effect (death, game end).
func (c *ClientConnection) clearEffects() {
	if c.server == nil || c.server.effects == nil {
		return
	}
	c.server.effects.remove(c, nil)
}

// activeEffects lists the player's current effects.
func (c *ClientConnection) activeEffects() []effect.Active {
	if c.server == nil || c.server.effects == nil {
		return nil
	}
	return c.server.effects.Active(c)
}

// effectLevel is the player's level of e (0 = none).
func (c *ClientConnection) effectLevel(e effect.Effect) int {
	if c.server == nil || c.server.Effects == nil {
		return 0
	}
	return c.server.Effects.Level(c, e)
}

// resendEffects re-sends every active effect to the client — Respawn makes
// the client forget them, so resyncView calls this.
func (c *ClientConnection) resendEffects() {
	if c.player == nil {
		return
	}
	for _, ae := range c.activeEffects() {
		_ = c.safeWrite(packetid.CbPlayEntityEffect, effect.EntityPayload(c.player.EntityID, ae.Effect, ae.Level, ae.Remaining))
	}
	_ = c.sendMovementAttributes() // Respawn reset the attribute too
	c.syncAbsorption(true)         // …and the yellow hearts (player metadata)
}

// effectsTick runs every tick on the instance: expires effects whose end
// must be visible, and heals players with Regeneration (level I: 1 HP every
// 50 ticks, each further level halves the interval, like vanilla).
func (i *Instance) effectsTick(tick uint64) {
	if i.Server == nil || i.Server.effects == nil {
		return
	}
	if tick%20 == 0 {
		i.Server.effects.sweep()
	}
	for _, c := range i.Players.snapshot() {
		p := c.player
		if p == nil || p.IsDead() {
			continue
		}
		lvl := c.effectLevel(effect.Regeneration)
		if lvl <= 0 {
			continue
		}
		if tick%effect.RegenInterval(lvl) != 0 {
			continue
		}
		if h := p.Health(); h < player.MaxHealth {
			p.SetHealth(min(h+1, player.MaxHealth))
			_ = c.sendSetHealth(p.Health())
		}
	}
}

// entityFlags is the player's metadata index-0 bitmask: on fire (0x01)
// and invisible (0x20).
func (c *ClientConnection) entityFlags() byte {
	var flags byte
	if c.fireTicks.Load() > 0 {
		flags |= 0x01
	}
	if c.effectLevel(effect.Invisibility) > 0 {
		flags |= 0x20
	}
	return flags
}

// broadcastEntityFlags sends the player's current entity flags to everyone
// in the instance, the player's own client included (it renders the
// first-person fire overlay from them).
func (c *ClientConnection) broadcastEntityFlags() {
	if c.player == nil || c.instance == nil {
		return
	}
	c.instance.Players.Broadcast(packetid.CbPlaySetEntityMetadata, entityFlagsPayload(c.player.EntityID, c.entityFlags()), -1)
}

// --- dig-side glue ---------------------------------------------------------

// digContext gathers the modifiers for this player's current dig.
func (c *ClientConnection) digContext() world.DigContext {
	held := c.inv.Held(c.heldSlot.Load())
	helmet := c.inv.Get(inventory.HelmetSlot)
	ctx := world.DigContext{OnGround: true}
	if c.server != nil {
		ctx.Efficiency = c.server.Enchantments.Level(held, enchant.Efficiency)
		ctx.AquaAffinity = c.server.Enchantments.Level(helmet, enchant.AquaAffinity) > 0
		ctx.Haste = c.server.Effects.Level(c, effect.Haste)
		ctx.MiningFatigue = c.server.Effects.Level(c, effect.MiningFatigue)
	}
	if c.player != nil {
		s := c.player.Snapshot()
		ctx.OnGround = c.serverOnGround() // not the packet's flag
		ctx.InWater = c.instance != nil &&
			c.instance.World.GetBlock(world.Position{X: floorF(s.X), Y: floorF(s.Y + eyeHeight), Z: floorF(s.Z)}).Name == world.Water.Name
	}
	return ctx
}

// damageHeldTool wears the held tool by n points (Unbreaking may spare it),
// breaking it — slot emptied, break sound — when its durability runs out.
// Non-tools are untouched.
func (c *ClientConnection) damageHeldTool(n int) {
	slot := int16(inventory.HotbarStart) + int16(c.heldSlot.Load())
	st := c.inv.Get(slot)
	if st.Empty() {
		return
	}
	name, ok := world.ItemName(st.ID)
	if !ok {
		return
	}
	maxDamage := world.ToolDurability(name)
	if maxDamage == 0 {
		return
	}
	if lvl := c.enchantOf(st, enchant.Unbreaking); lvl > 0 && enchant.UnbreakingSpares(lvl) {
		return // Unbreaking: only 1/(lvl+1) of hits wear the tool
	}
	st.Damage += n
	if st.Damage >= maxDamage {
		st = inventory.Stack{}
		if c.player != nil {
			s := c.player.Snapshot()
			_ = c.safeWrite(packetid.CbPlaySoundEffect,
				sound.EffectPayload("minecraft:entity.item.break", sound.CategoryPlayer, s.X, s.Y, s.Z, 0.8, 1))
		}
	}
	c.inv.Set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
	c.equipmentChanged()
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
