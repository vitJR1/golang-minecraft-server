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
