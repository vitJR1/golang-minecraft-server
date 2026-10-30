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
	// (server/attributes); haste / mining fatigue feed the dig formula,
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

// timed is one timed effect on a player.
type timed struct {
	level   int // amplifier+1
	expires time.Time
}

// Table holds timed effects per player key. Expired entries read as 0 and
// are dropped lazily (Level) or by Sweep. Safe for concurrent use; the zero
// value is ready.
type Table[K comparable] struct {
	mu      sync.Mutex
	players map[K]map[string]timed
}

// Level is k's amplifier+1 of e (0 = none).
func (t *Table[K]) Level(k K, e Effect) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	ae, ok := t.players[k][e.Name]
	if !ok {
		return 0
	}
	if time.Now().After(ae.expires) {
		delete(t.players[k], e.Name)
		return 0
	}
	return ae.level
}

// Apply grants k effect e at level (amplifier+1) for d.
func (t *Table[K]) Apply(k K, e Effect, level int, d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.players == nil {
		t.players = map[K]map[string]timed{}
	}
	if t.players[k] == nil {
		t.players[k] = map[string]timed{}
	}
	t.players[k][e.Name] = timed{level: level, expires: time.Now().Add(d)}
}

// Active lists k's unexpired effects, sorted by name.
func (t *Table[K]) Active(k K) []Active {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	out := make([]Active, 0, len(t.players[k]))
	for name, ae := range t.players[k] {
		if !now.Before(ae.expires) {
			continue
		}
		out = append(out, Active{Effect: Known[name], Level: ae.level, Remaining: ae.expires.Sub(now)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Effect.Name < out[j].Effect.Name })
	return out
}

// Sweep drops every expired effect and returns them per player — the lazy
// expiry in Level isn't enough for effects whose end must be visible.
func (t *Table[K]) Sweep() map[K][]Effect {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	expired := map[K][]Effect{}
	for k, effects := range t.players {
		for name, ae := range effects {
			if !now.Before(ae.expires) {
				delete(effects, name)
				expired[k] = append(expired[k], Known[name])
			}
		}
		if len(effects) == 0 {
			delete(t.players, k)
		}
	}
	return expired
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
