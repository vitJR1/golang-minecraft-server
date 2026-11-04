// Package effect is potion effects, independent of connections: the effects
// the server knows (with their registry ids), a Table of timed effects per
// player, the Entity Effect / Remove Entity Effect payloads, Regeneration's
// heal interval, the Absorption pool and its metadata, and the effect of a
// potion. The server applies their consequences (server/modifiers.go,
// attributes.go, absorption.go).
package effect

import (
	"bytes"
	"sort"
	"strings"
	"sync"
	"time"

	"minecraft-server/protocol"
)

// Effect is a potion effect id with its vanilla numeric registry id (the
// Entity Effect packet wants the number).
type Effect struct {
	Name string
	ID   int32
}

var (
	Speed         = Effect{"speed", 1}
	Slowness      = Effect{"slowness", 2}
	Haste         = Effect{"haste", 3}
	MiningFatigue = Effect{"mining_fatigue", 4}
	JumpBoost     = Effect{"jump_boost", 8}
	Regeneration  = Effect{"regeneration", 10}
	Invisibility  = Effect{"invisibility", 14}
	Blindness     = Effect{"blindness", 15}
	Absorption    = Effect{"absorption", 22}

	// Known is what /effect and games can grant. Jump boost / blindness are
	// applied by the client itself once it has the Entity Effect packet;
	// speed / slowness need the movement_speed attribute synced as well
	// (server/gameplay/attributes); haste / mining fatigue feed the dig formula,
	// regeneration heals (RegenInterval), invisibility flips the entity
	// flag, absorption fills the player's yellow-heart pool (AbsorptionPool,
	// synced through player metadata).
	Known = map[string]Effect{
		Speed.Name:         Speed,
		Slowness.Name:      Slowness,
		Haste.Name:         Haste,
		MiningFatigue.Name: MiningFatigue,
		JumpBoost.Name:     JumpBoost,
		Regeneration.Name:  Regeneration,
		Invisibility.Name:  Invisibility,
		Blindness.Name:     Blindness,
		Absorption.Name:    Absorption,
	}
)

// Names lists the known effect names, sorted — for /effect help and
// tab-completion.
func Names() []string {
	names := make([]string, 0, len(Known))
	for n := range Known {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Active is one effect currently on a player, as games see it.
type Active struct {
	Effect    Effect
	Level     int // amplifier+1
	Remaining time.Duration
}

// timed is one timed effect on a player. hidden is a weaker effect of the
// same kind that outlasts it (vanilla's hiddenEffect): it takes over when
// this one ends. Its expiry is absolute, so it runs down meanwhile, as in
// vanilla.
type timed struct {
	level   int // amplifier+1
	expires time.Time
	hidden  *timed
}

// live is the effect in force at now: t, or the first unexpired effect of
// its hidden chain.
func (t timed) live(now time.Time) (timed, bool) {
	for {
		if now.Before(t.expires) {
			return t, true
		}
		if t.hidden == nil {
			return timed{}, false
		}
		t = *t.hidden
	}
}

// update merges other into cur (both in force) like vanilla's
// MobEffectInstance.update: a higher level takes over (cur hidden under it
// when cur outlasts it), the same level extends to the later end, a lower
// level that outlasts cur waits hidden under it.
func (cur timed) update(other timed) timed {
	switch {
	case other.level > cur.level:
		next := other
		next.hidden = cur.hidden
		if other.expires.Before(cur.expires) {
			h := cur
			next.hidden = &h
		}
		return next
	case !other.expires.After(cur.expires):
		return cur // shorter and no stronger: nothing changes
	case other.level == cur.level:
		cur.expires = other.expires
		return cur
	case cur.hidden == nil:
		h := other
		cur.hidden = &h
		return cur
	default:
		h := cur.hidden.update(other)
		cur.hidden = &h
		return cur
	}
}

// Table holds timed effects per player key. Expired entries read as 0 but
// stay in the table until Sweep (or Remove / Forget) drops them: Sweep is
// what reports an ended effect, so the server can send Remove Entity Effect
// and resync attributes — a read must never swallow that. Safe for
// concurrent use; the zero value is ready.
type Table[K comparable] struct {
	mu      sync.Mutex
	players map[K]map[string]timed
}

// Level is k's amplifier+1 of e (0 = none or expired). Read-only: an
// expired effect is left for Sweep to report.
func (t *Table[K]) Level(k K, e Effect) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	ae, ok := t.players[k][e.Name]
	if !ok {
		return 0
	}
	ae, ok = ae.live(time.Now())
	if !ok {
		return 0
	}
	return ae.level
}

// Apply grants k effect e at level (amplifier+1) for d, combined with what
// k already has like vanilla's MobEffectInstance.update (timed.update): a
// higher level replaces the current effect, the same level extends it to
// the later end, a lower level never weakens or shortens a stronger one but
// waits hidden under it if it lasts longer, resumed by Sweep. It returns
// the effect k ends up with — what the client must be told, which isn't
// necessarily (level, d).
func (t *Table[K]) Apply(k K, e Effect, level int, d time.Duration) (int, time.Duration) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.players == nil {
		t.players = map[K]map[string]timed{}
	}
	if t.players[k] == nil {
		t.players[k] = map[string]timed{}
	}
	next := timed{level: level, expires: now.Add(d)}
	if cur, ok := t.players[k][e.Name]; ok {
		if cur, ok = cur.live(now); ok {
			next = cur.update(next)
		}
	}
	t.players[k][e.Name] = next
	return next.level, next.expires.Sub(now)
}

// Active lists k's unexpired effects, sorted by name.
func (t *Table[K]) Active(k K) []Active {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	out := make([]Active, 0, len(t.players[k]))
	for name, ae := range t.players[k] {
		ae, ok := ae.live(now)
		if !ok {
			continue
		}
		out = append(out, Active{Effect: Known[name], Level: ae.level, Remaining: ae.expires.Sub(now)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Effect.Name < out[j].Effect.Name })
	return out
}

// Sweep drops every expired effect and returns them per player, so the
// server can show each end (Remove Entity Effect, attributes, flags). An
// expired effect with a hidden one still in force is replaced by it and
// returned in resumed instead — the client must be shown the weaker level.
func (t *Table[K]) Sweep() (expired map[K][]Effect, resumed map[K][]Active) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	expired, resumed = map[K][]Effect{}, map[K][]Active{}
	for k, effects := range t.players {
		for name, ae := range effects {
			if now.Before(ae.expires) {
				continue
			}
			if next, ok := ae.live(now); ok {
				effects[name] = next
				resumed[k] = append(resumed[k], Active{Effect: Known[name], Level: next.level, Remaining: next.expires.Sub(now)})
				continue
			}
			delete(effects, name)
			expired[k] = append(expired[k], Known[name])
		}
		if len(effects) == 0 {
			delete(t.players, k)
		}
	}
	return expired, resumed
}

// Remove clears one effect (or all, when e is nil) from k and returns what
// was cleared.
func (t *Table[K]) Remove(k K, e *Effect) []Effect {
	t.mu.Lock()
	defer t.mu.Unlock()
	var cleared []Effect
	if e == nil {
		for name := range t.players[k] {
			cleared = append(cleared, Known[name])
		}
		delete(t.players, k)
	} else if _, ok := t.players[k][e.Name]; ok {
		delete(t.players[k], e.Name)
		cleared = append(cleared, *e)
	}
	return cleared
}

// Forget drops a player's effects (disconnect) without reporting them.
func (t *Table[K]) Forget(k K) {
	t.mu.Lock()
	delete(t.players, k)
	t.mu.Unlock()
}

// EntityPayload builds Entity Effect (1.20.1): entity id, effect id,
// amplifier, duration ticks, flags (show particles + icon), no factor data.
func EntityPayload(eid int32, e Effect, level int, d time.Duration) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, eid)
	protocol.WriteVarInt32ToBuffer(&buf, e.ID)
	buf.WriteByte(byte(level - 1))
	protocol.WriteVarInt32ToBuffer(&buf, int32(d/(50*time.Millisecond)))
	buf.WriteByte(0x02 | 0x04)
	buf.WriteByte(0) // has factor data: false
	return buf.Bytes()
}

// RemovePayload builds Remove Entity Effect: entity id, effect id.
func RemovePayload(eid int32, e Effect) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, eid)
	protocol.WriteVarInt32ToBuffer(&buf, e.ID)
	return buf.Bytes()
}

// RegenInterval is how many ticks Regeneration at level (amplifier+1) takes
// per 1 HP: 50 at level I, each further level halving it (like vanilla).
func RegenInterval(level int) uint64 {
	interval := uint64(50) >> uint(min(level-1, 5))
	if interval < 1 {
		interval = 1
	}
	return interval
}

// AbsorptionPerLevel is the pool each Absorption level grants (4 = two hearts).
const AbsorptionPerLevel float32 = 4

// AbsorptionPool is the pool after Absorption at level: topped up to the
// level's size, never shrinking a larger pool cur.
func AbsorptionPool(level int, cur float32) float32 {
	return max(AbsorptionPerLevel*float32(level), cur)
}

// absorptionMetaIndex is the Player metadata index of "Additional Hearts".
const absorptionMetaIndex byte = 15

// AbsorptionPayload builds Set Entity Metadata with the single Player field
// "Additional Hearts" (index 15, type 3 = Float). The client shows the
// yellow hearts only from this, not from the Entity Effect packet.
func AbsorptionPayload(eid int32, absorption float32) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, eid)
	buf.WriteByte(absorptionMetaIndex)
	protocol.WriteVarInt32ToBuffer(&buf, 3) // type: Float
	buf.Write(protocol.WriteFloat(absorption))
	buf.WriteByte(0xFF)
	return buf.Bytes()
}

// FromPotion maps a vanilla potion id (NBT "Potion") to the effect it
// applies, its level and duration. Only the kinds a mini-game server hands
// out are covered.
func FromPotion(potion string) (Effect, int, time.Duration, bool) {
	switch strings.TrimPrefix(potion, "minecraft:") {
	case "swiftness":
		return Speed, 1, 3 * time.Minute, true
	case "long_swiftness":
		return Speed, 1, 8 * time.Minute, true
	case "strong_swiftness":
		return Speed, 2, 90 * time.Second, true
	case "leaping":
		return JumpBoost, 1, 3 * time.Minute, true
	case "long_leaping":
		return JumpBoost, 1, 8 * time.Minute, true
	case "strong_leaping":
		return JumpBoost, 2, 90 * time.Second, true
	case "invisibility":
		return Invisibility, 1, 3 * time.Minute, true
	case "long_invisibility":
		return Invisibility, 1, 8 * time.Minute, true
	case "regeneration":
		return Regeneration, 1, 45 * time.Second, true
	case "strong_regeneration":
		return Regeneration, 2, 22 * time.Second, true
	case "slowness":
		return Slowness, 1, 90 * time.Second, true
	}
	return Effect{}, 0, 0, false
}
