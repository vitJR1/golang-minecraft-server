// Package player models a connected player's gameplay state — identity,
// position, gamemode, eventually inventory. Wire-level concerns (the TCP
// connection, encryption, compression, packet framing) stay on
// server.ClientConnection and are deliberately not referenced here.
package player

import "sync"

// Gamemode mirrors vanilla's 1-byte gamemode field.
type Gamemode byte

const (
	Survival  Gamemode = 0
	Creative  Gamemode = 1
	Adventure Gamemode = 2
	Spectator Gamemode = 3
)

// MaxHealth is the full-health value (20 = ten hearts), matching vanilla.
// Stored as the cap the combat system clamps to on spawn/respawn.
const MaxHealth float32 = 20

// Snapshot is an immutable point-in-time view of mutable Player fields.
// Obtained via Player.Snapshot — safe to pass around and read freely without
// holding any lock. The identity fields (EntityID/Name/UUID) are duplicated
// here for callers that want one consistent struct.
type Snapshot struct {
	EntityID int32
	Name     string
	UUID     [16]byte

	X, Y, Z    float64
	Yaw, Pitch float32
	OnGround   bool
	Gamemode   Gamemode

	canFly   bool
	// Health is current hit points in [0, MaxHealth]. Dead is true once a
	// killing blow lands and stays true until Respawn resets it — used to
	// reject further hits on a corpse and to gate the respawn flow.
	Health float32
	Dead   bool
}

// Player is the per-connection gameplay entity. Identity fields
// (EntityID/Name/UUID) are set once in New and never modified — safe to
// read without a lock. The mutable state (position, rotation, gamemode)
// lives behind mu; reach for it via Snapshot/MoveTo/LookAt/SetGamemode.
type Player struct {
	EntityID int32
	Name     string
	UUID     [16]byte // binary form, ready for the wire

	mu       sync.RWMutex
	x, y, z  float64
	yaw      float32
	pitch    float32
	onGround bool
	gamemode Gamemode

	canFly   bool
	// Combat state. health is current HP; dead latches after a killing
	// blow. lastAttackTick is the instance tick of this player's most
	// recent attack (drives the 1.9 attack-cooldown damage scaling).
	// lastHurtTick / lastHurtAmount implement vanilla's per-target
	// invulnerability window: within ~10 ticks of a hit, a new hit only
	// lands if it's stronger, and only for the difference.
	health         float32
	dead           bool
	lastAttackTick uint64
	lastHurtTick   uint64
	lastHurtAmount float32
}

// New constructs a Player at the default spawn (0.5, 67, 0.5) in
// Adventure mode. Adventure = no block break/place client-side either,
// which suits the hub/auth flow (mini-game arenas can switch their
// players into Survival/Creative themselves).
func New(entityID int32, name string, uuid [16]byte) *Player {
	return &Player{
		EntityID: entityID,
		Name:     name,
		UUID:     uuid,
		y:        67,
		x:        0.5,
		z:        0.5,
		gamemode: Adventure,
		health:   MaxHealth,
	}
}

// Snapshot returns a consistent copy of every mutable field plus identity.
// Use this when you need multiple fields read together (e.g. when building
// a Spawn Player or Teleport Entity packet) — separate getters would let
// concurrent updates split the read across boundary.
func (p *Player) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return Snapshot{
		EntityID: p.EntityID,
		Name:     p.Name,
		UUID:     p.UUID,
		X:        p.x,
		Y:        p.y,
		Z:        p.z,
		Yaw:      p.yaw,
		Pitch:    p.pitch,
		OnGround: p.onGround,
		Gamemode: p.gamemode,
		Health:   p.health,
		Dead:     p.dead,
	}
}

// MoveTo updates position and ground state without touching rotation.
// Matches the shape of the Set Player Position serverbound packet.
func (p *Player) MoveTo(x, y, z float64, onGround bool) {
	p.mu.Lock()
	p.x, p.y, p.z, p.onGround = x, y, z, onGround
	p.mu.Unlock()
}

// MoveAndLook updates position, rotation, and ground state in one
// critical section. Matches Set Player Position and Rotation.
func (p *Player) MoveAndLook(x, y, z float64, yaw, pitch float32, onGround bool) {
	p.mu.Lock()
	p.x, p.y, p.z = x, y, z
	p.yaw, p.pitch = yaw, pitch
	p.onGround = onGround
	p.mu.Unlock()
}

// LookAt updates rotation and ground state without touching position.
// Matches Set Player Rotation.
func (p *Player) LookAt(yaw, pitch float32, onGround bool) {
	p.mu.Lock()
	p.yaw, p.pitch, p.onGround = yaw, pitch, onGround
	p.mu.Unlock()
}

// SetGamemode swaps the player's gamemode. The change is local; broadcasts
// (Set Game Mode packet) are the caller's responsibility.
func (p *Player) SetGamemode(g Gamemode) {
	p.mu.Lock()
	p.gamemode = g
	p.mu.Unlock()
}

func (p *Player) ToggleFly() (canFly bool) {
	p.mu.Lock()
	p.canFly = !p.canFly
	canFly = p.canFly
	p.mu.Unlock()
	return
}
// Health returns the player's current hit points.
func (p *Player) Health() float32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.health
}

// IsDead reports whether the player is currently on the death screen.
func (p *Player) IsDead() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dead
}

// SetHealth sets HP directly, clamped to [0, MaxHealth]. Used by regen and
// admin tooling; combat damage goes through ApplyDamage. Does not touch the
// dead flag — Respawn is the only thing that clears it.
func (p *Player) SetHealth(h float32) {
	if h < 0 {
		h = 0
	}
	if h > MaxHealth {
		h = MaxHealth
	}
	p.mu.Lock()
	p.health = h
	p.mu.Unlock()
}

// ApplyDamage subtracts amount from health under vanilla's per-target
// invulnerability window. now is the current instance tick; invulnTicks is
// the window length (vanilla: 10). Within the window a fresh hit only lands
// if it exceeds the last hit's amount, and then only for the difference —
// this is what lets a faster weapon "re-hit" through i-frames but blocks
// rapid spam. Returns the damage actually applied, the resulting health,
// and whether this blow was lethal. A no-op (applied==0) when the hit is
// swallowed by i-frames or the player is already dead.
func (p *Player) ApplyDamage(amount float32, now uint64, invulnTicks uint64) (applied, health float32, killed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dead || amount <= 0 {
		return 0, p.health, false
	}

	if now-p.lastHurtTick < invulnTicks && now >= p.lastHurtTick {
		// Still invulnerable: only the surplus over the prior hit lands.
		if amount <= p.lastHurtAmount {
			return 0, p.health, false
		}
		applied = amount - p.lastHurtAmount
		p.lastHurtAmount = amount
	} else {
		applied = amount
		p.lastHurtTick = now
		p.lastHurtAmount = amount
	}

	p.health -= applied
	if p.health <= 0 {
		p.health = 0
		p.dead = true
		killed = true
	}
	return applied, p.health, killed
}

// AttackCooldownTick records that this player just attacked at tick now, and
// returns the previous attack tick so the caller can compute the cooldown
// charge (1.9 attack-strength scaling) before the update.
func (p *Player) AttackCooldownTick(now uint64) (prev uint64) {
	p.mu.Lock()
	prev = p.lastAttackTick
	p.lastAttackTick = now
	p.mu.Unlock()
	return prev
}

// Respawn resets the player to full health and clears the dead flag. Caller
// is responsible for the position reset and the wire packets.
func (p *Player) Respawn() {
	p.mu.Lock()
	p.health = MaxHealth
	p.dead = false
	p.lastHurtTick = 0
	p.lastHurtAmount = 0
	p.mu.Unlock()
}
