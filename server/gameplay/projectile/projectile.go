// Package projectile is the flight of thrown things and arrows, independent
// of connections and instances: entity types, the per-tick motion (drag,
// gravity, a lifetime), walking a step for the first solid block so a fast
// arrow can't tunnel through a wall (March), the facing a projectile renders
// with, its Spawn / Teleport payloads, and the bow and arrow rules. The
// server keeps the flights per instance and applies their effects
// (server/projectile.go, arrow.go).
package projectile

import (
	"bytes"
	"math"

	"minecraft-server/protocol"
)

// Projectile entity type ids (protocol 763), from minecraft-data.
const (
	EggEntityID        int32 = 24
	SnowballEntityID   int32 = 92
	EnderPearlEntityID int32 = 28
	FireballEntityID   int32 = 57
	ArrowEntityID      int32 = 3
)

// Throw/flight tuning (blocks per tick). Roughly matches vanilla thrown items.
const (
	ThrowSpeed   = 1.5
	throwGravity = 0.03 // subtracted from vy each tick
	throwDrag    = 0.99 // velocity retained each tick
	maxTicks     = 200  // ~10s safety cap so a stray projectile can't live forever
)

// Fireball (fire charge thrown in the air): straight flight at FireballSpeed
// for up to FireballLife ticks, then a power-1.5 explosion with 1.5× the
// knockback (vanilla ghast fireball feel).
const (
	FireballSpeed     = 1.0
	FireballLife      = 100
	FireballPower     = 1.5
	FireballKnockback = 1.5
)

// ThrowableEntityID maps a throwable item id to its projectile entity type
// id, returning ok=false for non-throwable items.
func ThrowableEntityID(item string) (int32, bool) {
	switch item {
	case "minecraft:egg":
		return EggEntityID, true
	case "minecraft:snowball":
		return SnowballEntityID, true
	case "minecraft:ender_pearl":
		return EnderPearlEntityID, true
	default:
		return 0, false
	}
}

// Flight is one projectile's identity and motion.
type Flight struct {
	EID    int32
	UUID   [16]byte
	TypeID int32
	// Data is the Spawn Entity data field (arrows: the owner's entity id).
	Data       int32
	X, Y, Z    float64
	VX, VY, VZ float64
	Ticks      int
	// NoGravity keeps the projectile on a straight line (fireballs);
	// Gravity / Drag override the thrown-item defaults when non-zero;
	// MaxTicks overrides the default lifetime when > 0.
	NoGravity     bool
	Gravity, Drag float64
	MaxTicks      int

	yaw, pitch float32 // last facing sent (Angles)
}

// Advance applies one tick of drag and gravity (unless the projectile flies
// straight), counts the tick, and returns where the projectile would move.
func (f *Flight) Advance() (nx, ny, nz float64) {
	drag, gravity := throwDrag, throwGravity
	if f.Drag != 0 {
		drag = f.Drag
	}
	if f.Gravity != 0 {
		gravity = f.Gravity
	}
	f.VX *= drag
	f.VZ *= drag
	if f.NoGravity {
		f.VY *= drag
	} else {
		f.VY = f.VY*drag - gravity
	}
	f.Ticks++
	return f.X + f.VX, f.Y + f.VY, f.Z + f.VZ
}

// Expired: the projectile has flown its lifetime.
func (f *Flight) Expired() bool {
	life := maxTicks
	if f.MaxTicks > 0 {
		life = f.MaxTicks
	}
	return f.Ticks >= life
}

// marchStep is the sampling distance of March; fine enough that no full
// block is skipped at any projectile speed.
const marchStep = 0.1

// March walks the segment (x0,y0,z0)→(x1,y1,z1) in marchStep increments and
// reports the first solid point on it: the last free point before it and
// true, or the end point and false when nothing is in the way.
func March(solid func(x, y, z float64) bool, x0, y0, z0, x1, y1, z1 float64) (float64, float64, float64, bool) {
	dx, dy, dz := x1-x0, y1-y0, z1-z0
	length := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if length == 0 {
		return x1, y1, z1, solid(x1, y1, z1)
	}
	steps := int(math.Ceil(length / marchStep))
	px, py, pz := x0, y0, z0
	for n := 1; n <= steps; n++ {
		f := float64(n) / float64(steps)
		cx, cy, cz := x0+dx*f, y0+dy*f, z0+dz*f
		if solid(cx, cy, cz) {
			return px, py, pz, true
		}
		px, py, pz = cx, cy, cz
	}
	return x1, y1, z1, false
}

// Angles is the yaw/pitch (degrees) the projectile faces, derived from its
// velocity the way vanilla's AbstractArrow.tick does — yaw = atan2(vx, vz),
// pitch = atan2(vy, horizontal) — so arrows render along their flight
// instead of all pointing the same way. A resting projectile keeps the
// angles of its last move.
func (f *Flight) Angles() (yaw, pitch float32) {
	h := math.Sqrt(f.VX*f.VX + f.VZ*f.VZ)
	if h == 0 && f.VY == 0 {
		return f.yaw, f.pitch
	}
	f.yaw = float32(math.Atan2(f.VX, f.VZ) * 180 / math.Pi)
	f.pitch = float32(math.Atan2(f.VY, h) * 180 / math.Pi)
	return f.yaw, f.pitch
}

// SpawnPayload builds Spawn Entity (0x01) for the projectile, carrying its
// initial velocity so the client animates the throw.
func (f *Flight) SpawnPayload() []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, f.EID)
	buf.Write(f.UUID[:])
	protocol.WriteVarInt32ToBuffer(&buf, f.TypeID)
	buf.Write(protocol.WriteDouble(f.X))
	buf.Write(protocol.WriteDouble(f.Y))
	buf.Write(protocol.WriteDouble(f.Z))
	yaw, pitch := f.Angles()
	buf.WriteByte(protocol.AngleToByte(pitch))
	buf.WriteByte(protocol.AngleToByte(yaw))
	buf.WriteByte(protocol.AngleToByte(yaw))     // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, f.Data) // data (arrows: owner id)
	buf.Write(protocol.WriteShort(protocol.VelocityShort(f.VX)))
	buf.Write(protocol.WriteShort(protocol.VelocityShort(f.VY)))
	buf.Write(protocol.WriteShort(protocol.VelocityShort(f.VZ)))
	return buf.Bytes()
}

// TeleportPayload builds Teleport Entity (0x68) for the projectile at its
// current position.
func (f *Flight) TeleportPayload() []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, f.EID)
	buf.Write(protocol.WriteDouble(f.X))
	buf.Write(protocol.WriteDouble(f.Y))
	buf.Write(protocol.WriteDouble(f.Z))
	yaw, pitch := f.Angles()
	buf.WriteByte(protocol.AngleToByte(yaw))
	buf.WriteByte(protocol.AngleToByte(pitch))
	buf.WriteByte(0) // on ground = false
	return buf.Bytes()
}
