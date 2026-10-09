package server

import (
	"bytes"
	"math/rand"
	"sync"
	"time"

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
	EffectHaste         = Effect{"haste", 3}
	EffectMiningFatigue = Effect{"mining_fatigue", 4}

	// knownEffects is what /effect accepts.
	knownEffects = map[string]Effect{
		EffectHaste.Name:         EffectHaste,
		EffectMiningFatigue.Name: EffectMiningFatigue,
	}
)

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
	if c.player == nil {
		return
	}
	for _, e := range cleared {
		var buf bytes.Buffer
		protocol.WriteVarInt32ToBuffer(&buf, c.player.EntityID)
		protocol.WriteVarInt32ToBuffer(&buf, e.ID)
		_ = c.safeWrite(CbPlayRemoveEntityEffect, buf.Bytes())
	}
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
}
