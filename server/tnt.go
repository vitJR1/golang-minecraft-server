package server

import (
	"bytes"
	"math"
	"math/rand"

	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// tnt.go: vanilla-style TNT. Lighting a TNT block (flint and steel or fire
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
//   - players: within 2*power blocks, damage = ((impact²+impact)/2*7*2*power
//     + 1) where impact = (1 - dist/(2*power)) * exposure (fraction of the
//     hitbox the explosion can see), knockback = direction*impact delivered
//     through the Explosion packet's player-motion fields. Only when the
//     instance has combat enabled; creative/spectator players are immune.
//   - other primed TNT in range is knocked away like vanilla.
//
// The client plays the explosion sound and particles itself when it receives
// the Explosion packet, and removes the listed blocks locally; we still
// broadcast the block changes so late joiners and chunk re-sends agree.

const (
	tntFuseTicks    = 80   // vanilla fuse when lit by hand
	tntPower        = 4.0  // explosion strength
	tntGravity      = 0.04 // per tick
	tntDrag         = 0.98 // velocity retained per tick
	tntGroundDrag   = 0.7  // extra horizontal friction while resting
	tntHeight       = 0.98 // entity height; explosion centre sits 1/16 of it up
	tntRayStep      = 0.3
	tntRayDecay     = 0.225
	tntExposureStep = 0.25 // ray-march step for the hitbox exposure test
	// explosionPacketRange is how far the Explosion packet (particles,
	// sound, local block removal) is sent, like vanilla's 64 blocks.
	explosionPacketRange = 64.0
)

// primedTNT is one lit TNT entity.
type primedTNT struct {
	eid        int32
	uuid       [16]byte
	x, y, z    float64
	vx, vy, vz float64
	fuse       int
	onGround   bool
	// igniter is who lit it (or whose explosion did); nil when unknown.
	// Used as the actor for block-break vetoes and as the killer on a
	// lethal blast.
	igniter *ClientConnection
}

// tryIgniteTNT handles a right-click with an igniter item on a TNT block:
// primes it, wears the flint and steel (or consumes the fire charge) and
// reports whether the click was handled.
func (c *ClientConnection) tryIgniteTNT(pos world.Position, held string) bool {
	if held != "minecraft:flint_and_steel" && held != "minecraft:fire_charge" {
		return false
	}
	if c.instance == nil || c.instance.World.GetBlock(pos).Name != world.TNT.Name {
		return false
	}
	if !c.instance.allowBlockBreak(c, pos) {
		_ = c.sendBlockUpdate(pos, c.instance.World.GetBlock(pos))
		return true
	}
	if !c.instance.primeTNT(pos, tntFuseTicks, c) {
		return false
	}
	if held == "minecraft:flint_and_steel" {
		if c.gamemode() != player.Creative {
			c.damageHeldTool(1)
		}
	} else {
		c.consumeHeld()
	}
	return true
}

// primeTNT replaces the TNT block at pos with a primed entity carrying the
// given fuse, giving it vanilla's small random hop. Returns false when pos
// doesn't hold TNT.
func (i *Instance) primeTNT(pos world.Position, fuse int, igniter *ClientConnection) bool {
	if i.World.GetBlock(pos).Name != world.TNT.Name || i.Server == nil {
		return false
	}
	i.SetBlock(pos, world.Air)

	angle := rand.Float64() * 2 * math.Pi
	t := &primedTNT{
		eid:     i.Server.nextEntityID.Add(1),
		x:       float64(pos.X) + 0.5,
		y:       float64(pos.Y),
		z:       float64(pos.Z) + 0.5,
		vx:      -math.Sin(angle) * 0.02,
		vy:      0.2,
		vz:      -math.Cos(angle) * 0.02,
		fuse:    fuse,
		igniter: igniter,
	}
	t.uuid = entityUUID(t.eid)

	i.tntMu.Lock()
	i.tnts = append(i.tnts, t)
	i.tntMu.Unlock()

	i.Players.Broadcast(CbPlaySpawnEntity, spawnTNTPayload(t), -1)
	i.Players.Broadcast(CbPlaySetEntityMetadata, tntMetadataPayload(t), -1)
	i.playSound("minecraft:entity.tnt.primed", soundCategoryBlocks, t.x, t.y, t.z, 1, 1)
	return true
}

// passableForEntity reports whether an entity can occupy the block at
// (x,y,z): air and fluids, unlike solidAt which treats water as a wall.
func (i *Instance) passableForEntity(x, y, z float64) bool {
	b := i.World.GetBlock(world.Position{X: floorF(x), Y: floorF(y), Z: floorF(z)})
	return b == world.Air || b.Name == world.Water.Name || b.Name == world.Lava.Name
}

// tntTick moves every primed TNT one step and explodes the ones whose fuse
// ran out. Explosions run outside tntMu because they may prime more TNT.
func (i *Instance) tntTick(uint64) {
	i.tntMu.Lock()
	if len(i.tnts) == 0 {
		i.tntMu.Unlock()
		return
	}
	var exploding []*primedTNT
	var removed []int32
	kept := i.tnts[:0]
	for _, t := range i.tnts {
		i.stepTNT(t)
		t.fuse--
		switch {
		case t.y < -64:
			removed = append(removed, t.eid)
		case t.fuse <= 0:
			removed = append(removed, t.eid)
			exploding = append(exploding, t)
		default:
			kept = append(kept, t)
			i.Players.Broadcast(CbPlayTeleportEntity, tntTeleportPayload(t), -1)
		}
	}
	for j := len(kept); j < len(i.tnts); j++ {
		i.tnts[j] = nil
	}
	i.tnts = kept
	i.tntMu.Unlock()

	if len(removed) > 0 {
		i.Players.Broadcast(CbPlayRemoveEntities, removeEntitiesPayload(removed), -1)
	}
	for _, t := range exploding {
		i.explode(t.x, t.y+tntHeight*0.0625, t.z, tntPower, t.igniter)
	}
}

// stepTNT integrates one tick of primed-TNT motion: gravity, axis-wise block
// collision (a hit on an axis zeroes that component), drag, ground friction.
func (i *Instance) stepTNT(t *primedTNT) {
	t.vy -= tntGravity

	nx := t.x + t.vx
	if t.vx != 0 && !i.passableForEntity(nx, t.y+0.1, t.z) {
		t.vx = 0
	} else {
		t.x = nx
	}
	nz := t.z + t.vz
	if t.vz != 0 && !i.passableForEntity(t.x, t.y+0.1, nz) {
		t.vz = 0
	} else {
		t.z = nz
	}
	ny := t.y + t.vy
	switch {
	case t.vy < 0 && !i.passableForEntity(t.x, ny, t.z):
		t.y = math.Floor(ny) + 1
		t.vy = 0
		t.onGround = true
	case t.vy > 0 && !i.passableForEntity(t.x, ny+tntHeight, t.z):
		t.vy = 0
	default:
		t.y = ny
		t.onGround = false
	}

	t.vx *= tntDrag
	t.vy *= tntDrag
	t.vz *= tntDrag
	if t.onGround {
		t.vx *= tntGroundDrag
		t.vz *= tntGroundDrag
		if math.Abs(t.vx) < 1e-3 {
			t.vx = 0
		}
		if math.Abs(t.vz) < 1e-3 {
			t.vz = 0
		}
	}
}

// explode runs a vanilla-shaped explosion of the given power at (x,y,z).
// igniter is the responsible player (may be nil).
func (i *Instance) explode(x, y, z, power float64, igniter *ClientConnection) {
	actor := igniter
	if actor != nil && (actor.instance != i || actor.isClosed()) {
		actor = nil
	}

	destroyed := i.explosionBlocks(x, y, z, power, actor)
	i.applyExplosionBlocks(destroyed, power, actor)

	hits := i.explosionPlayers(x, y, z, power, actor)
	i.knockbackTNT(x, y, z, power)

	// One Explosion packet per player within explosionPacketRange (vanilla:
	// 64 blocks), carrying that player's own knockback (the client applies
	// it to itself); everyone else's motion is implied by their position
	// updates, as in vanilla.
	records := explosionRecords(x, y, z, destroyed)
	for _, c := range i.Players.snapshot() {
		if c.player == nil {
			continue
		}
		var mx, my, mz float64
		if h, ok := hits[c]; ok {
			mx, my, mz = h.vx, h.vy, h.vz
		} else {
			s := c.player.Snapshot()
			dx, dy, dz := s.X-x, s.Y-y, s.Z-z
			if dx*dx+dy*dy+dz*dz > explosionPacketRange*explosionPacketRange {
				continue
			}
		}
		_ = c.safeWrite(CbPlayExplosion, explosionPayload(x, y, z, float32(power), records, mx, my, mz))
	}
}

// explosionBlocks ray-marches the 16³ shell and returns the blocks the
// explosion destroys, already filtered through the block-break veto chain.
// Nothing is destroyed when the centre is in water.
func (i *Instance) explosionBlocks(x, y, z, power float64, actor *ClientConnection) []world.Position {
	centre := i.World.GetBlock(world.Position{X: floorF(x), Y: floorF(y), Z: floorF(z)})
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
				dx, dy, dz = dx/n*tntRayStep, dy/n*tntRayStep, dz/n*tntRayStep
				h := power * (0.7 + rand.Float64()*0.6)
				px, py, pz := x, y, z
				for h > 0 {
					pos := world.Position{X: floorF(px), Y: floorF(py), Z: floorF(pz)}
					b := i.World.GetBlock(pos)
					if b != world.Air {
						h -= (world.BlastResistance(b.Name) + 0.3) * 0.3
					}
					if h > 0 && b != world.Air {
						set[pos] = struct{}{}
					}
					px, py, pz = px+dx, py+dy, pz+dz
					h -= tntRayDecay
				}
			}
		}
	}
	out := make([]world.Position, 0, len(set))
	for pos := range set {
		if i.allowExplosionBreak(actor, pos) {
			out = append(out, pos)
		}
	}
	return out
}

// allowExplosionBreak runs the block-break veto chain for an explosion. With
// no known actor the hooks see a nil player; a hook that can't cope panics,
// which safeVeto turns into "allow" — same policy as every other hook.
func (i *Instance) allowExplosionBreak(actor *ClientConnection, pos world.Position) bool {
	if actor != nil {
		return i.allowBlockBreak(actor, pos)
	}
	return safeVeto(i, "explosion OnBlockBreak", func() bool { return i.allowBlockBreak(nil, pos) })
}

// applyExplosionBlocks clears the destroyed blocks: TNT is primed with a
// short fuse, everything else drops its item with chance 1/power.
func (i *Instance) applyExplosionBlocks(destroyed []world.Position, power float64, actor *ClientConnection) {
	if len(destroyed) == 0 {
		return
	}
	changes := make([]world.BlockChange, 0, len(destroyed))
	var chain []world.Position
	for _, pos := range destroyed {
		b := i.World.GetBlock(pos)
		if b == world.Air {
			continue
		}
		if b.Name == world.TNT.Name {
			chain = append(chain, pos)
			continue
		}
		if rand.Float64() < 1/power {
			if item, n := world.BlockDrop(b.Name); item != "" {
				i.DropItem(float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, item, n)
			}
		}
		changes = append(changes, world.BlockChange{Pos: pos, Block: world.Air})
	}
	i.SetBlocks(changes)
	for _, pos := range chain {
		// Vanilla: nextInt(fuse/4) + fuse/8 → 10..29 ticks.
		i.primeTNT(pos, rand.Intn(tntFuseTicks/4)+tntFuseTicks/8, actor)
	}
}

// explosionHit is one player's share of an explosion.
type explosionHit struct {
	vx, vy, vz float64
}

// explosionPlayers damages and knocks back the players in range, returning
// each one's knockback vector for the Explosion packet. Requires combat on;
// creative and spectator players are immune.
func (i *Instance) explosionPlayers(x, y, z, power float64, actor *ClientConnection) map[*ClientConnection]explosionHit {
	hits := map[*ClientConnection]explosionHit{}
	if !i.PvPEnabled() {
		return hits
	}
	radius := power * 2
	now := i.Tick()
	for _, c := range i.Players.snapshot() {
		p := c.player
		if p == nil || p.IsDead() {
			continue
		}
		s := p.Snapshot()
		if s.Gamemode == player.Creative || s.Gamemode == player.Spectator {
			continue
		}
		dx, dy, dz := s.X-x, s.Y-y, s.Z-z
		dist := math.Sqrt(dx*dx+dy*dy+dz*dz) / radius
		if dist > 1 {
			continue
		}
		// Direction from the blast to the player's eyes.
		ex, ey, ez := s.X-x, s.Y+eyeHeight-y, s.Z-z
		n := math.Sqrt(ex*ex + ey*ey + ez*ez)
		if n == 0 {
			continue
		}
		ex, ey, ez = ex/n, ey/n, ez/n
		exposure := i.explosionExposure(x, y, z, s)
		impact := (1 - dist) * exposure
		if impact <= 0 {
			continue
		}
		damage := float32(math.Floor((impact*impact+impact)/2*7*radius + 1))
		applied, newHealth, killed := p.ApplyDamage(damage, now, i.Combat.InvulnTicks)
		hits[c] = explosionHit{vx: ex * impact, vy: ey * impact, vz: ez * impact}
		if applied <= 0 {
			continue
		}
		i.Players.Broadcast(CbPlayHurtAnimation, hurtAnimationPayload(s.EntityID, 0), -1)
		i.playSound("minecraft:entity.player.hurt", soundCategoryPlayer, s.X, s.Y, s.Z, 1, 1)
		if killed {
			var killer *ClientConnection
			if actor != nil && actor != c {
				killer = actor
			}
			c.die(killer)
		} else {
			_ = c.sendSetHealth(newHealth)
		}
	}
	return hits
}

// explosionExposure is the fraction of sample points over the player's
// hitbox that the blast centre can see without a solid block in between
// (vanilla getSeenPercent, with a coarse ray march).
func (i *Instance) explosionExposure(x, y, z float64, s player.Snapshot) float64 {
	const (
		w = 2 * playerHalfWidth
		h = playerHeight
	)
	stepX := 1 / (w*2 + 1)
	stepY := 1 / (h*2 + 1)
	seen, total := 0, 0
	for fx := 0.0; fx <= 1; fx += stepX {
		for fy := 0.0; fy <= 1; fy += stepY {
			for fz := 0.0; fz <= 1; fz += stepX {
				px := s.X - playerHalfWidth + fx*w
				py := s.Y + fy*h
				pz := s.Z - playerHalfWidth + fz*w
				total++
				if i.rayClear(x, y, z, px, py, pz) {
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

// rayClear reports whether the segment from (x0,y0,z0) to (x1,y1,z1) crosses
// no solid (non-air, non-fluid) block.
func (i *Instance) rayClear(x0, y0, z0, x1, y1, z1 float64) bool {
	dx, dy, dz := x1-x0, y1-y0, z1-z0
	length := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if length == 0 {
		return true
	}
	steps := int(math.Ceil(length / tntExposureStep))
	for n := 1; n <= steps; n++ {
		f := float64(n) / float64(steps)
		if !i.passableForEntity(x0+dx*f, y0+dy*f, z0+dz*f) {
			return false
		}
	}
	return true
}

// knockbackTNT shoves other primed TNT within range away from the blast.
func (i *Instance) knockbackTNT(x, y, z, power float64) {
	radius := power * 2
	i.tntMu.Lock()
	defer i.tntMu.Unlock()
	for _, t := range i.tnts {
		dx, dy, dz := t.x-x, t.y+tntHeight/2-y, t.z-z
		n := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if n == 0 || n/radius > 1 {
			continue
		}
		impact := 1 - n/radius
		t.vx += dx / n * impact
		t.vy += dy / n * impact
		t.vz += dz / n * impact
		t.onGround = false
	}
}

// --- wire payloads ---------------------------------------------------------

// explosionRecord is a destroyed block as a signed offset from the blast
// centre's block, the form the Explosion packet carries.
type explosionRecord [3]int8

// explosionRecords converts destroyed positions to packet records, dropping
// any that don't fit the int8 offsets (never happens at power 4).
func explosionRecords(x, y, z float64, destroyed []world.Position) []explosionRecord {
	bx, by, bz := floorF(x), floorF(y), floorF(z)
	out := make([]explosionRecord, 0, len(destroyed))
	for _, p := range destroyed {
		ox, oy, oz := p.X-bx, p.Y-by, p.Z-bz
		if ox < -128 || ox > 127 || oy < -128 || oy > 127 || oz < -128 || oz > 127 {
			continue
		}
		out = append(out, explosionRecord{int8(ox), int8(oy), int8(oz)})
	}
	return out
}

// explosionPayload builds Explosion (0x1B): centre, strength, records and the
// receiving player's own motion delta.
func explosionPayload(x, y, z float64, strength float32, records []explosionRecord, mx, my, mz float64) []byte {
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

// spawnTNTPayload builds Spawn Entity for a primed TNT.
func spawnTNTPayload(t *primedTNT) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, t.eid)
	buf.Write(t.uuid[:])
	protocol.WriteVarInt32ToBuffer(&buf, world.TNTEntityID)
	buf.Write(protocol.WriteDouble(t.x))
	buf.Write(protocol.WriteDouble(t.y))
	buf.Write(protocol.WriteDouble(t.z))
	buf.WriteByte(0)                        // pitch
	buf.WriteByte(0)                        // yaw
	buf.WriteByte(0)                        // head yaw
	protocol.WriteVarInt32ToBuffer(&buf, 0) // data
	buf.Write(protocol.WriteShort(velocityShort(t.vx)))
	buf.Write(protocol.WriteShort(velocityShort(t.vy)))
	buf.Write(protocol.WriteShort(velocityShort(t.vz)))
	return buf.Bytes()
}

// tntMetadataPayload builds Set Entity Metadata with the fuse (index 8,
// VarInt) so the client animates the flashing countdown.
func tntMetadataPayload(t *primedTNT) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, t.eid)
	buf.WriteByte(8)                        // index: Fuse
	protocol.WriteVarInt32ToBuffer(&buf, 1) // type: VarInt
	protocol.WriteVarInt32ToBuffer(&buf, int32(t.fuse))
	buf.WriteByte(0xFF)
	return buf.Bytes()
}

// tntTeleportPayload builds Teleport Entity for a primed TNT's position.
func tntTeleportPayload(t *primedTNT) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, t.eid)
	buf.Write(protocol.WriteDouble(t.x))
	buf.Write(protocol.WriteDouble(t.y))
	buf.Write(protocol.WriteDouble(t.z))
	buf.WriteByte(0) // yaw
	buf.WriteByte(0) // pitch
	if t.onGround {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

// sendTNTEntities streams every primed TNT to this client (join/respawn).
func (c *ClientConnection) sendTNTEntities() error {
	type outPkt struct {
		id      int32
		payload []byte
	}
	c.instance.tntMu.Lock()
	pkts := make([]outPkt, 0, 2*len(c.instance.tnts))
	for _, t := range c.instance.tnts {
		pkts = append(pkts,
			outPkt{CbPlaySpawnEntity, spawnTNTPayload(t)},
			outPkt{CbPlaySetEntityMetadata, tntMetadataPayload(t)})
	}
	c.instance.tntMu.Unlock()
	for _, p := range pkts {
		if err := c.safeWrite(p.id, p.payload); err != nil {
			return err
		}
	}
	return nil
}
