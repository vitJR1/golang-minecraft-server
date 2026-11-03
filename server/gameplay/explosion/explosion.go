// Package explosion is vanilla-style TNT and explosions, independent of
// connections and instances. Lighting a TNT block (flint and steel or fire
// charge on it, or being caught in another explosion) replaces the block
// with a primed TNT entity that falls, slides and counts a fuse down on the
// instance tick loop. At zero it explodes with power 4:
//
//   - blocks: 1352 rays from the centre (the 16×16×16 shell), each with
//     intensity power*(0.7..1.3), stepping 0.3 blocks and losing
//     (blastResistance+0.3)*0.3 per block crossed plus 0.225 per step; a
//     block a ray still has intensity on is destroyed. Destroyed blocks drop
//     their item with probability 1/power; a destroyed TNT block is primed
//     with a short random fuse instead (chain reaction). Every destruction
//     runs the OnBlockBreak veto chain with the igniter as the actor, so
//     game rules (BedWars beds, hub protection) hold. Underwater explosions
//     break nothing.
//   - players: within 2*power blocks, damage =
//     ((impact²+impact)/2*7*2*power + 1) × the instance's ExplosionDamage
//     multiplier (1 = vanilla), where impact = (1 - dist/(2*power)) * exposure (fraction of the
//     hitbox the explosion can see), knockback = direction*impact delivered
//     through the Explosion packet's player-motion fields. Only when the
//     instance has combat enabled; creative/spectator players are immune.
//   - other primed TNT in range is knocked away like vanilla.
//
// The client plays the explosion sound and particles itself when it receives
// the Explosion packet, and removes the listed blocks locally; we still
// broadcast the block changes so late joiners and chunk re-sends agree.
//
// This package holds the physics and the maths — the primed TNT entity and its
// motion, the block ray march, the hitbox exposure, impact and damage, and the
// wire payloads; server/tnt.go applies them (vetoes, players, broadcasts).
package explosion

import (
	"bytes"
	"math"
	"math/rand"

	"minecraft-server/anticheat"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

const (
	FuseTicks = 80   // vanilla fuse when lit by hand
	Height    = 0.98 // primed TNT entity height; the blast centre sits 1/16 of it up
	// PacketRange is how far the Explosion packet (particles, sound, local
	// block removal) is sent, like vanilla's 64 blocks.
	PacketRange = 64.0

	gravity      = 0.04 // per tick
	drag         = 0.98 // velocity retained per tick
	groundDrag   = 0.7  // extra horizontal friction while resting
	rayStep      = 0.3
	rayDecay     = 0.225
	exposureStep = 0.25 // ray-march step for the hitbox exposure test
)

// BlockAt reads a block; PassAt reports whether an entity may occupy the
// block at a point (Passable of the block there).
type (
	BlockAt func(world.Position) world.Block
	PassAt  func(x, y, z float64) bool
)

// Passable reports whether an entity can occupy b: air and fluids (unlike a
// solid-block test, which treats water as a wall).
func Passable(b world.Block) bool {
	return b == world.Air || b.Name == world.Water.Name || b.Name == world.Lava.Name
}

// TNT is one lit TNT entity.
type TNT struct {
	EID        int32
	UUID       [16]byte
	X, Y, Z    float64
	VX, VY, VZ float64
	Fuse       int
	OnGround   bool
}

// Prime is a TNT lit at block pos with the given fuse, with vanilla's small
// random hop.
func Prime(eid int32, uuid [16]byte, pos world.Position, fuse int) TNT {
	angle := rand.Float64() * 2 * math.Pi
	return TNT{
		EID:  eid,
		UUID: uuid,
		X:    float64(pos.X) + 0.5,
		Y:    float64(pos.Y),
		Z:    float64(pos.Z) + 0.5,
		VX:   -math.Sin(angle) * 0.02,
		VY:   0.2,
		VZ:   -math.Cos(angle) * 0.02,
		Fuse: fuse,
	}
}

// ChainFuse is the fuse of a TNT block caught in a blast: vanilla's
// nextInt(fuse/4) + fuse/8, 10..29 ticks.
func ChainFuse() int { return rand.Intn(FuseTicks/4) + FuseTicks/8 }

// Centre is where the TNT explodes from.
func (t *TNT) Centre() (x, y, z float64) { return t.X, t.Y + Height*0.0625, t.Z }

// Step integrates one tick of primed-TNT motion: gravity, axis-wise block
// collision (a hit on an axis zeroes that component), drag, ground friction.
func (t *TNT) Step(pass PassAt) {
	t.VY -= gravity

	nx := t.X + t.VX
	if t.VX != 0 && !pass(nx, t.Y+0.1, t.Z) {
		t.VX = 0
	} else {
		t.X = nx
	}
	nz := t.Z + t.VZ
	if t.VZ != 0 && !pass(t.X, t.Y+0.1, nz) {
		t.VZ = 0
	} else {
		t.Z = nz
	}
	ny := t.Y + t.VY
	switch {
	case t.VY < 0 && !pass(t.X, ny, t.Z):
		t.Y = math.Floor(ny) + 1
		t.VY = 0
		t.OnGround = true
	case t.VY > 0 && !pass(t.X, ny+Height, t.Z):
		t.VY = 0
	default:
		t.Y = ny
		t.OnGround = false
	}

	t.VX *= drag
	t.VY *= drag
	t.VZ *= drag
	if t.OnGround {
		t.VX *= groundDrag
		t.VZ *= groundDrag
		if math.Abs(t.VX) < 1e-3 {
			t.VX = 0
		}
		if math.Abs(t.VZ) < 1e-3 {
			t.VZ = 0
		}
	}
}

// Shove knocks the TNT away from a blast of power at (x, y, z), when in range.
func (t *TNT) Shove(x, y, z, power float64) {
	radius := power * 2
	dx, dy, dz := t.X-x, t.Y+Height/2-y, t.Z-z
	n := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if n == 0 || n/radius > 1 {
		return
	}
	impact := 1 - n/radius
	t.VX += dx / n * impact
	t.VY += dy / n * impact
	t.VZ += dz / n * impact
	t.OnGround = false
}

// Blocks ray-marches the 16³ shell from (x, y, z) and returns the blocks an
// explosion of power destroys (before any game veto). Nothing is destroyed
// when the centre is in water.
func Blocks(at BlockAt, x, y, z, power float64) []world.Position {
	centre := at(world.Position{X: floorF(x), Y: floorF(y), Z: floorF(z)})
	if centre.Name == world.Water.Name {
		return nil
	}
	set := map[world.Position]struct{}{}
	for j := 0; j < 16; j++ {
		for k := 0; k < 16; k++ {
			for l := 0; l < 16; l++ {
				if j != 0 && j != 15 && k != 0 && k != 15 && l != 0 && l != 15 {
					continue
				}
				dx := float64(j)/15*2 - 1
				dy := float64(k)/15*2 - 1
				dz := float64(l)/15*2 - 1
				n := math.Sqrt(dx*dx + dy*dy + dz*dz)
				dx, dy, dz = dx/n*rayStep, dy/n*rayStep, dz/n*rayStep
				h := power * (0.7 + rand.Float64()*0.6)
				px, py, pz := x, y, z
				for h > 0 {
					pos := world.Position{X: floorF(px), Y: floorF(py), Z: floorF(pz)}
					b := at(pos)
					if b != world.Air {
						h -= (world.BlastResistance(b.Name) + 0.3) * 0.3
					}
					if h > 0 && b != world.Air {
						set[pos] = struct{}{}
					}
					px, py, pz = px+dx, py+dy, pz+dz
					h -= rayDecay
				}
			}
		}
	}
	out := make([]world.Position, 0, len(set))
	for pos := range set {
		out = append(out, pos)
	}
	return out
}

// Exposure is the fraction of sample points over a player's hitbox (feet at
// fx, fy, fz) that the blast centre can see without a solid block in between
// (vanilla getSeenPercent, with a coarse ray march).
func Exposure(pass PassAt, x, y, z, fx, fy, fz float64) float64 {
	const (
		half = anticheat.PlayerWidth / 2
		w    = anticheat.PlayerWidth
		h    = anticheat.PlayerHeight
	)
	stepX := 1 / (w*2 + 1)
	stepY := 1 / (h*2 + 1)
	seen, total := 0, 0
	for sx := 0.0; sx <= 1; sx += stepX {
		for sy := 0.0; sy <= 1; sy += stepY {
			for sz := 0.0; sz <= 1; sz += stepX {
				px := fx - half + sx*w
				py := fy + sy*h
				pz := fz - half + sz*w
				total++
				if RayClear(pass, x, y, z, px, py, pz) {
					seen++
				}
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(seen) / float64(total)
}

// RayClear reports whether the segment from (x0,y0,z0) to (x1,y1,z1) crosses
// no solid (non-air, non-fluid) block.
func RayClear(pass PassAt, x0, y0, z0, x1, y1, z1 float64) bool {
	dx, dy, dz := x1-x0, y1-y0, z1-z0
	length := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if length == 0 {
		return true
	}
	steps := int(math.Ceil(length / exposureStep))
	for n := 1; n <= steps; n++ {
		f := float64(n) / float64(steps)
		if !pass(x0+dx*f, y0+dy*f, z0+dz*f) {
			return false
		}
	}
	return true
}

// Impact is what a blast of power at (x, y, z) does to a player with feet at
// (fx, fy, fz) and the given exposure: impact (0..1) and the knockback
// direction toward the eyes. ok is false out of range or with no impact.
func Impact(x, y, z, power, fx, fy, fz, exposure float64) (impact float64, dir anticheat.Vec, ok bool) {
	radius := power * 2
	dx, dy, dz := fx-x, fy-y, fz-z
	dist := math.Sqrt(dx*dx+dy*dy+dz*dz) / radius
	if dist > 1 {
		return 0, dir, false
	}
	ex, ey, ez := fx-x, fy+anticheat.EyeHeight-y, fz-z
	n := math.Sqrt(ex*ex + ey*ey + ez*ez)
	if n == 0 {
		return 0, dir, false
	}
	impact = (1 - dist) * exposure
	if impact <= 0 {
		return 0, dir, false
	}
	return impact, anticheat.Vec{X: ex / n, Y: ey / n, Z: ez / n}, true
}

// InRange reports whether a player with feet at (fx, fy, fz) is within a
// blast of power at (x, y, z) at all (before the exposure is worth casting).
func InRange(x, y, z, power, fx, fy, fz float64) bool {
	dx, dy, dz := fx-x, fy-y, fz-z
	return math.Sqrt(dx*dx+dy*dy+dz*dz)/(power*2) <= 1
}

// Damage is the vanilla player damage of an impact (before armor and any
// per-instance multiplier).
func Damage(impact, power float64) float64 {
	return math.Floor((impact*impact+impact)/2*7*(power*2) + 1)
}

func floorF(v float64) int { return int(math.Floor(v)) }

// --- wire payloads ---------------------------------------------------------

// Record is a destroyed block as a signed offset from the blast
// centre's block, the form the Explosion packet carries.
type Record [3]int8

// Records converts destroyed positions to packet records, dropping
// any that don't fit the int8 offsets (never happens at power 4).
func Records(x, y, z float64, destroyed []world.Position) []Record {
	bx, by, bz := floorF(x), floorF(y), floorF(z)
	out := make([]Record, 0, len(destroyed))
	for _, p := range destroyed {
		ox, oy, oz := p.X-bx, p.Y-by, p.Z-bz
		if ox < -128 || ox > 127 || oy < -128 || oy > 127 || oz < -128 || oz > 127 {
			continue
		}
		out = append(out, Record{int8(ox), int8(oy), int8(oz)})
	}
	return out
}

// Payload builds Explosion (0x1B): centre, strength, records and the
// receiving player's own motion delta.
func Payload(x, y, z float64, strength float32, records []Record, mx, my, mz float64) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteDouble(x))
	buf.Write(protocol.WriteDouble(y))
	buf.Write(protocol.WriteDouble(z))
	buf.Write(protocol.WriteFloat(strength))
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(records)))
	for _, r := range records {
		buf.WriteByte(byte(r[0]))
		buf.WriteByte(byte(r[1]))
		buf.WriteByte(byte(r[2]))
	}
	buf.Write(protocol.WriteFloat(float32(mx)))
	buf.Write(protocol.WriteFloat(float32(my)))
	buf.Write(protocol.WriteFloat(float32(mz)))
	return buf.Bytes()
}

// SpawnPayload builds Spawn Entity for the primed TNT.
func (t *TNT) SpawnPayload() []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, t.EID)
	buf.Write(t.UUID[:])
	protocol.WriteVarInt32ToBuffer(&buf, world.TNTEntityID)
	buf.Write(protocol.WriteDouble(t.X))
	buf.Write(protocol.WriteDouble(t.Y))
	buf.Write(protocol.WriteDouble(t.Z))
	buf.WriteByte(0)                        // pitch
	buf.WriteByte(0)                        // yaw
	buf.WriteByte(0)                        // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, 0) // data
	buf.Write(protocol.WriteShort(protocol.VelocityShort(t.VX)))
	buf.Write(protocol.WriteShort(protocol.VelocityShort(t.VY)))
	buf.Write(protocol.WriteShort(protocol.VelocityShort(t.VZ)))
	return buf.Bytes()
}

// MetadataPayload builds Set Entity Metadata with the fuse (index 8,
// VarInt) so the client animates the flashing countdown.
func (t *TNT) MetadataPayload() []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, t.EID)
	buf.WriteByte(8)                        // index: Fuse
	protocol.WriteVarInt32ToBuffer(&buf, 1) // type: VarInt
	protocol.WriteVarInt32ToBuffer(&buf, int32(t.Fuse))
	buf.WriteByte(0xFF)
	return buf.Bytes()
}

// TeleportPayload builds Teleport Entity for the TNT's position.
func (t *TNT) TeleportPayload() []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, t.EID)
	buf.Write(protocol.WriteDouble(t.X))
	buf.Write(protocol.WriteDouble(t.Y))
	buf.Write(protocol.WriteDouble(t.Z))
	buf.WriteByte(0) // yaw
	buf.WriteByte(0) // pitch
	if t.OnGround {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}
