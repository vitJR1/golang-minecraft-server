package server

import (
	"math/rand"
	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/combat"
	"minecraft-server/server/explosion"
	"minecraft-server/server/sound"
	"minecraft-server/world"
)

// tnt.go: TNT and explosions in an instance (the physics and maths are
// server/explosion): lighting TNT, ticking the primed entities, and an
// explosion's effects — the game's break vetoes, drops, chain reactions,
// player damage and knockback, the Explosion packets.

// tntAutoFuse is the fuse of a TNT block a game lights as it is placed
// (Instance.SetTNTAutoPrime).
const tntAutoFuse = 50

// primedTNT is one lit TNT entity and who lit it (or whose explosion did;
// nil when unknown) — the actor for block-break vetoes and the killer on a
// lethal blast.
type primedTNT struct {
	explosion.TNT
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
	if !c.instance.primeTNT(pos, explosion.FuseTicks, c) {
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

	eid := i.Server.nextEntityID.Add(1)
	t := &primedTNT{TNT: explosion.Prime(eid, entityUUID(eid), pos, fuse), igniter: igniter}

	i.tntMu.Lock()
	i.tnts = append(i.tnts, t)
	i.tntMu.Unlock()

	i.Players.Broadcast(packetid.CbPlaySpawnEntity, t.SpawnPayload(), -1)
	i.Players.Broadcast(packetid.CbPlaySetEntityMetadata, t.MetadataPayload(), -1)
	i.playSound("minecraft:entity.tnt.primed", sound.CategoryBlocks, t.X, t.Y, t.Z, 1, 1)
	return true
}

// passableForEntity reports whether an entity can occupy the block at
// (x,y,z): air and fluids, unlike solidAt which treats water as a wall.
func (i *Instance) passableForEntity(x, y, z float64) bool {
	return explosion.Passable(i.World.GetBlock(world.Position{X: floorF(x), Y: floorF(y), Z: floorF(z)}))
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
		t.Step(i.passableForEntity)
		t.Fuse--
		switch {
		case t.Y < -64:
			removed = append(removed, t.EID)
		case t.Fuse <= 0:
			removed = append(removed, t.EID)
			exploding = append(exploding, t)
		default:
			kept = append(kept, t)
			i.Players.Broadcast(packetid.CbPlayTeleportEntity, t.TeleportPayload(), -1)
		}
	}
	for j := len(kept); j < len(i.tnts); j++ {
		i.tnts[j] = nil
	}
	i.tnts = kept
	i.tntMu.Unlock()

	if len(removed) > 0 {
		i.Players.Broadcast(packetid.CbPlayRemoveEntities, removeEntitiesPayload(removed), -1)
	}
	for _, t := range exploding {
		x, y, z := t.Centre()
		i.explode(x, y, z, world.TNTPower, t.igniter)
	}
}

// explode runs a vanilla-shaped explosion of the given power at (x,y,z).
// igniter is the responsible player (may be nil).
func (i *Instance) explode(x, y, z, power float64, igniter *ClientConnection) {
	i.explodeWith(x, y, z, power, 1, igniter)
}

// explodeWith is explode with the player knockback scaled by knockback
// (fireballs shove 1.5× harder than their power suggests).
func (i *Instance) explodeWith(x, y, z, power, knockback float64, igniter *ClientConnection) {
	actor := igniter
	if actor != nil && (actor.instance != i || actor.isClosed()) {
		actor = nil
	}

	// Vanilla order: decide which blocks go, hurt the players while those
	// blocks still stand (exposure is cast through the intact world, so a
	// wall shelters whoever hides behind it), then clear the blocks.
	destroyed := i.explosionBlocks(x, y, z, power, actor)
	hits := i.explosionPlayers(x, y, z, power, actor)
	for c, h := range hits {
		hits[c] = explosionHit{vx: h.vx * knockback, vy: h.vy * knockback, vz: h.vz * knockback}
	}
	i.applyExplosionBlocks(destroyed, power, actor)
	i.knockbackTNT(x, y, z, power)

	// One Explosion packet per player within explosion.PacketRange (vanilla:
	// 64 blocks), carrying that player's own knockback (the client applies
	// it to itself); everyone else's motion is implied by their position
	// updates, as in vanilla.
	records := explosion.Records(x, y, z, destroyed)
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
			if dx*dx+dy*dy+dz*dz > explosion.PacketRange*explosion.PacketRange {
				continue
			}
		}
		_ = c.safeWrite(packetid.CbPlayExplosion, explosion.Payload(x, y, z, float32(power), records, mx, my, mz))
		if mx != 0 || my != 0 || mz != 0 {
			c.giveVelocity(mx, my, mz) // the explosion pushes the client itself
		}
	}
}

// explosionBlocks returns the blocks the explosion destroys (explosion.Blocks),
// filtered through the game's vetoes.
func (i *Instance) explosionBlocks(x, y, z, power float64, actor *ClientConnection) []world.Position {
	var out []world.Position
	for _, pos := range explosion.Blocks(i.World.GetBlock, x, y, z, power) {
		if i.allowExplosionBreak(actor, pos) {
			out = append(out, pos)
		}
	}
	return out
}

// allowExplosionBreak asks the explosion-specific veto (game.ExplosionBreakVeto,
// e.g. BedWars shielding beds) and then the block-break veto chain. With no
// known actor the hooks see a nil player; a hook that can't cope panics,
// which safeVeto turns into "allow" — same policy as every other hook.
func (i *Instance) allowExplosionBreak(actor *ClientConnection, pos world.Position) bool {
	if !i.allowExplosionBreakVeto(actor, pos) {
		return false
	}
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
		i.primeTNT(pos, explosion.ChainFuse(), actor)
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
		if !explosion.InRange(x, y, z, power, s.X, s.Y, s.Z) {
			continue
		}
		exposure := explosion.Exposure(i.passableForEntity, x, y, z, s.X, s.Y, s.Z)
		impact, dir, ok := explosion.Impact(x, y, z, power, s.X, s.Y, s.Z, exposure)
		if !ok {
			continue
		}
		damage := float32(explosion.Damage(impact, power) * i.ExplosionDamage())
		applied, newHealth, killed := p.ApplyDamage(c.absorbDamage(damage), now, i.Combat.InvulnTicks)
		hits[c] = explosionHit{vx: dir.X * impact, vy: dir.Y * impact, vz: dir.Z * impact}
		if applied <= 0 {
			continue
		}
		i.Players.Broadcast(packetid.CbPlayHurtAnimation, combat.HurtAnimationPayload(s.EntityID, 0), -1)
		i.playSound("minecraft:entity.player.hurt", sound.CategoryPlayer, s.X, s.Y, s.Z, 1, 1)
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

// knockbackTNT shoves other primed TNT within range away from the blast.
func (i *Instance) knockbackTNT(x, y, z, power float64) {
	i.tntMu.Lock()
	defer i.tntMu.Unlock()
	for _, t := range i.tnts {
		t.Shove(x, y, z, power)
	}
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
			outPkt{packetid.CbPlaySpawnEntity, t.SpawnPayload()},
			outPkt{packetid.CbPlaySetEntityMetadata, t.MetadataPayload()})
	}
	c.instance.tntMu.Unlock()
	for _, p := range pkts {
		if err := c.safeWrite(p.id, p.payload); err != nil {
			return err
		}
	}
	return nil
}
