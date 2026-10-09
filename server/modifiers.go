package server

import (
	"bytes"
	"math/rand"
	"sort"
	"sync"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// modifiers.go: the pluggable sources of dig/durability modifiers, with one
// simple implementation each —
//
//   - Enchantments reads a tool's levels. The shipped nbtEnchantments takes
//     them from the item's own NBT ("Enchantments" list), which is what a
//     creative-given or /give'd enchanted item carries. Efficiency speeds
//     digging up, Unbreaking spares durability, Aqua Affinity (helmet)
//     cancels the underwater penalty.
//   - Effects reads a player's potion effects. The shipped effectTable keeps
//     timed effects per connection, granted by /effect and shown on the
//     client through Entity Effect packets. Haste and Mining Fatigue feed
//     the dig formula.
//
// Swap either on the Server (Server.Enchantments / Server.Effects) for a
// richer implementation; the dig code only talks to the interfaces.

// Enchantment ids this server understands.
const (
	EnchantEfficiency   = "minecraft:efficiency"
	EnchantUnbreaking   = "minecraft:unbreaking"
	EnchantAquaAffinity = "minecraft:aqua_affinity"
)

// Enchantments answers "what level of enchantment e does this stack have".
type Enchantments interface {
	Level(st itemStack, enchantment string) int
}

// nbtEnchantments is the default Enchantments: levels parsed from item NBT.
type nbtEnchantments struct{}

func (nbtEnchantments) Level(st itemStack, enchantment string) int {
	return st.enchantLevel(enchantment)
}

// Effect is a potion effect id with its vanilla numeric registry id (the
// Entity Effect packet wants the number).
type Effect struct {
	Name string
	ID   int32
}

var (
	EffectSpeed         = Effect{"speed", 1}
	EffectSlowness      = Effect{"slowness", 2}
	EffectHaste         = Effect{"haste", 3}
	EffectMiningFatigue = Effect{"mining_fatigue", 4}
	EffectJumpBoost     = Effect{"jump_boost", 8}
	EffectRegeneration  = Effect{"regeneration", 10}
	EffectInvisibility  = Effect{"invisibility", 14}
	EffectBlindness     = Effect{"blindness", 15}

	// knownEffects is what /effect and games can grant. Speed / slowness /
	// jump boost / blindness are applied by the client itself once it has
	// the Entity Effect packet; haste / mining fatigue feed the dig formula,
	// regeneration heals on effectsTick, invisibility flips the entity flag.
	knownEffects = map[string]Effect{
		EffectSpeed.Name:         EffectSpeed,
		EffectSlowness.Name:      EffectSlowness,
		EffectHaste.Name:         EffectHaste,
		EffectMiningFatigue.Name: EffectMiningFatigue,
		EffectJumpBoost.Name:     EffectJumpBoost,
		EffectRegeneration.Name:  EffectRegeneration,
		EffectInvisibility.Name:  EffectInvisibility,
		EffectBlindness.Name:     EffectBlindness,
	}
)

// effectNames lists the known effect names, sorted — for /effect help and
// tab-completion.
func effectNames() []string {
	names := make([]string, 0, len(knownEffects))
	for n := range knownEffects {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ActiveEffect is one effect currently on a player, as games see it.
type ActiveEffect struct {
	Effect    Effect
	Level     int // amplifier+1
	Remaining time.Duration
}

// Effects answers "what amplifier+1 of effect does this player have now"
// (0 = none, 1 = level I, …).
type Effects interface {
	Level(c *ClientConnection, effect Effect) int
}

// activeEffect is one timed effect on a player.
type activeEffect struct {
	level   int // amplifier+1
	expires time.Time
}

// effectTable is the default Effects: a per-connection map of timed
// effects. Expired entries read as 0 and are dropped lazily.
type effectTable struct {
	mu      sync.Mutex
	players map[*ClientConnection]map[string]activeEffect
}

func newEffectTable() *effectTable {
	return &effectTable{players: map[*ClientConnection]map[string]activeEffect{}}
}

func (t *effectTable) Level(c *ClientConnection, effect Effect) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	ae, ok := t.players[c][effect.Name]
	if !ok {
		return 0
	}
	if time.Now().After(ae.expires) {
		delete(t.players[c], effect.Name)
		return 0
	}
	return ae.level
}

// apply grants effect at level (amplifier+1) for d, telling the client.
func (t *effectTable) apply(c *ClientConnection, effect Effect, level int, d time.Duration) {
	t.mu.Lock()
	if t.players[c] == nil {
		t.players[c] = map[string]activeEffect{}
	}
	t.players[c][effect.Name] = activeEffect{level: level, expires: time.Now().Add(d)}
	t.mu.Unlock()
	if c.player != nil {
		_ = c.safeWrite(CbPlayEntityEffect, entityEffectPayload(c.player.EntityID, effect, level, d))
	}
	if effect == EffectInvisibility {
		c.broadcastEntityFlags()
	}
}

// active returns the player's unexpired effects, sorted by name.
func (t *effectTable) active(c *ClientConnection) []ActiveEffect {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	out := make([]ActiveEffect, 0, len(t.players[c]))
	for name, ae := range t.players[c] {
		if !now.Before(ae.expires) {
			continue
		}
		out = append(out, ActiveEffect{Effect: knownEffects[name], Level: ae.level, Remaining: ae.expires.Sub(now)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Effect.Name < out[j].Effect.Name })
	return out
}

// sweep drops every expired effect, telling each client (and, for
// invisibility, everyone) — the lazy expiry in Level isn't enough for
// effects whose end must be visible.
func (t *effectTable) sweep() {
	now := time.Now()
	t.mu.Lock()
	expired := map[*ClientConnection][]Effect{}
	for c, effects := range t.players {
		for name, ae := range effects {
			if !now.Before(ae.expires) {
				delete(effects, name)
				expired[c] = append(expired[c], knownEffects[name])
			}
		}
		if len(effects) == 0 {
			delete(t.players, c)
		}
	}
	t.mu.Unlock()
	for c, list := range expired {
		c.sendEffectsRemoved(list)
	}
}

// remove clears one effect (or all, when effect is nil) and tells the client.
func (t *effectTable) remove(c *ClientConnection, effect *Effect) {
	t.mu.Lock()
	var cleared []Effect
	if effect == nil {
		for name := range t.players[c] {
			cleared = append(cleared, knownEffects[name])
		}
		delete(t.players, c)
	} else if _, ok := t.players[c][effect.Name]; ok {
		delete(t.players[c], effect.Name)
		cleared = append(cleared, *effect)
	}
	t.mu.Unlock()
	c.sendEffectsRemoved(cleared)
}

// sendEffectsRemoved tells the client these effects ended and refreshes the
// entity flags if invisibility was among them.
func (c *ClientConnection) sendEffectsRemoved(list []Effect) {
	if c.player == nil {
		return
	}
	invisible := false
	for _, e := range list {
		var buf bytes.Buffer
		protocol.WriteVarInt32ToBuffer(&buf, c.player.EntityID)
		protocol.WriteVarInt32ToBuffer(&buf, e.ID)
		_ = c.safeWrite(CbPlayRemoveEntityEffect, buf.Bytes())
		if e == EffectInvisibility {
			invisible = true
		}
	}
	if invisible {
		c.broadcastEntityFlags()
	}
}

// --- per-connection effect API (games bridge these) -------------------------

// applyEffect grants e at level (1 = I) for d. No-op without a server.
func (c *ClientConnection) applyEffect(e Effect, level int, d time.Duration) {
	if c.server == nil || c.server.effects == nil || level < 1 {
		return
	}
	c.server.effects.apply(c, e, level, d)
}

// removeEffect ends e now (no-op when absent).
func (c *ClientConnection) removeEffect(e Effect) {
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
func (c *ClientConnection) activeEffects() []ActiveEffect {
	if c.server == nil || c.server.effects == nil {
		return nil
	}
	return c.server.effects.active(c)
}

// effectLevel is the player's level of e (0 = none).
func (c *ClientConnection) effectLevel(e Effect) int {
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
		_ = c.safeWrite(CbPlayEntityEffect, entityEffectPayload(c.player.EntityID, ae.Effect, ae.Level, ae.Remaining))
	}
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
		lvl := c.effectLevel(EffectRegeneration)
		if lvl <= 0 {
			continue
		}
		interval := uint64(50) >> uint(min(lvl-1, 5))
		if interval < 1 {
			interval = 1
		}
		if tick%interval != 0 {
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
	if c.effectLevel(EffectInvisibility) > 0 {
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
	c.instance.Players.Broadcast(CbPlaySetEntityMetadata, entityFlagsPayload(c.player.EntityID, c.entityFlags()), -1)
}

// forget drops a disconnected player's effects.
func (t *effectTable) forget(c *ClientConnection) {
	t.mu.Lock()
	delete(t.players, c)
	t.mu.Unlock()
}

// entityEffectPayload builds Entity Effect (1.20.1): entity id, effect id,
// amplifier, duration ticks, flags (show particles + icon), no factor data.
func entityEffectPayload(eid int32, effect Effect, level int, d time.Duration) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, eid)
	protocol.WriteVarInt32ToBuffer(&buf, effect.ID)
	buf.WriteByte(byte(level - 1))
	protocol.WriteVarInt32ToBuffer(&buf, int32(d/(50*time.Millisecond)))
	buf.WriteByte(0x02 | 0x04)
	buf.WriteByte(0) // has factor data: false
	return buf.Bytes()
}

// --- dig-side glue ---------------------------------------------------------

// digContext gathers the modifiers for this player's current dig.
func (c *ClientConnection) digContext() world.DigContext {
	held := c.inv.held(c.heldSlot.Load())
	helmet := c.inv.get(armorHelmetSlot)
	ctx := world.DigContext{OnGround: true}
	if c.server != nil {
		ctx.Efficiency = c.server.Enchantments.Level(held, EnchantEfficiency)
		ctx.AquaAffinity = c.server.Enchantments.Level(helmet, EnchantAquaAffinity) > 0
		ctx.Haste = c.server.Effects.Level(c, EffectHaste)
		ctx.MiningFatigue = c.server.Effects.Level(c, EffectMiningFatigue)
	}
	if c.player != nil {
		s := c.player.Snapshot()
		ctx.OnGround = s.OnGround
		ctx.InWater = c.instance != nil &&
			c.instance.World.GetBlock(world.Position{X: floorF(s.X), Y: floorF(s.Y + eyeHeight), Z: floorF(s.Z)}).Name == world.Water.Name
	}
	return ctx
}

// armorHelmetSlot is the window-0 index of the helmet.
const armorHelmetSlot = 5

// damageHeldTool wears the held tool by n points (Unbreaking may spare it),
// breaking it — slot emptied, break sound — when its durability runs out.
// Non-tools are untouched.
func (c *ClientConnection) damageHeldTool(n int) {
	slot := int16(hotbarStart) + int16(c.heldSlot.Load())
	st := c.inv.get(slot)
	if st.empty() {
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
	if c.server != nil {
		if lvl := c.server.Enchantments.Level(st, EnchantUnbreaking); lvl > 0 && rand.Intn(lvl+1) > 0 {
			return // Unbreaking: only 1/(lvl+1) of hits wear the tool
		}
	}
	st.Damage += n
	if st.Damage >= maxDamage {
		st = itemStack{}
		if c.player != nil {
			s := c.player.Snapshot()
			_ = c.safeWrite(CbPlaySoundEffect,
				soundEffectPayload("minecraft:entity.item.break", soundCategoryPlayer, s.X, s.Y, s.Z, 0.8, 1))
		}
	}
	c.inv.set(slot, st)
	_ = c.sendSetSlot(0, slot, st)
	c.equipmentChanged()
}
