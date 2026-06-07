package server

import (
	"math"
	"minecraft-server/protocol"
	"sync"
)

// Zombie represents a server-side mob entity — position and entity ID only.
// No AI state is stored here; movement logic lives in ZombieManager.tick.
type Zombie struct {
	EntityID int32
	X, Y, Z  float64
}

// ZombieManager tracks all zombies in an instance and drives their movement
// and damage every tick via the instance tick loop.
type ZombieManager struct {
	mu      sync.RWMutex
	zombies []*Zombie
	inst    *Instance
}

// NewZombieManager creates a manager and registers its tick handler on inst.
// Call once per RedRoom instance; repeated calls add duplicate tick handlers.
func NewZombieManager(inst *Instance) *ZombieManager {
	zm := &ZombieManager{inst: inst}
	inst.OnTick(zm.tick)
	return zm
}

// Add registers a zombie with the manager so it participates in tick updates.
func (zm *ZombieManager) Add(z *Zombie) {
	zm.mu.Lock()
	zm.zombies = append(zm.zombies, z)
	zm.mu.Unlock()
}

// Clear removes all zombies from the manager (does not despawn them on the wire).
func (zm *ZombieManager) Clear() {
	zm.mu.Lock()
	zm.zombies = nil
	zm.mu.Unlock()
}

// tick runs at 20 Hz. For each zombie it finds the nearest living player,
// steps toward them at 0.1 blocks/tick, broadcasts position and head rotation
// packets, and applies 1 half-heart of damage with a swing animation once per
// second when within melee range.
func (zm *ZombieManager) tick(t uint64) {
	zm.mu.RLock()
	zombies := make([]*Zombie, len(zm.zombies))
	copy(zombies, zm.zombies)
	zm.mu.RUnlock()

	if len(zombies) == 0 {
		return
	}

	// Snapshot living players in this instance.
	var players []*ClientConnection
	zm.inst.Players.Range(func(c *ClientConnection) {
		if c.player != nil && !c.player.IsDead() {
			players = append(players, c)
		}
	})
	if len(players) == 0 {
		return
	}

	for _, z := range zombies {
		// Find the nearest player using squared distance — avoids sqrt.
		var target *ClientConnection
		minDistSq := math.MaxFloat64
		for _, p := range players {
			s := p.player.Snapshot()
			dx := s.X - z.X
			dy := s.Y - z.Y
			dz := s.Z - z.Z
			distSq := dx*dx + dy*dy + dz*dz
			if distSq < minDistSq {
				minDistSq = distSq
				target = p
			}
		}
		if target == nil {
			continue
		}

		s := target.player.Snapshot()
		dx := s.X - z.X
		dz := s.Z - z.Z
		distXZSq := dx*dx + dz*dz

		// Step toward the target on the XZ plane — sqrt needed for normalization.
		if distXZSq > 0.6*0.6 {
			distXZ := math.Sqrt(distXZSq)
			speed := 0.1
			z.X += (dx / distXZ) * speed
			z.Z += (dz / distXZ) * speed
		}

		// Separation force — push zombies apart when they overlap.
		for _, other := range zombies {
			if other == z {
				continue
			}
			sdx := z.X - other.X
			sdz := z.Z - other.Z
			sepSq := sdx*sdx + sdz*sdz
			if sepSq < 0.8*0.8 && sepSq > 0.001 {
				sepDist := math.Sqrt(sepSq)
				push := 0.05
				z.X += (sdx / sepDist) * push
				z.Z += (sdz / sepDist) * push
			}
		}

		// Face the target.
		yaw := float32(math.Atan2(-dx, dz) * (180.0 / math.Pi))

		// Broadcast updated position to every player in the instance.
		zm.inst.Players.Broadcast(CbPlayTeleportEntity, zombieTeleportPayload(z, yaw), -1)

		// Sync head rotation separately — TeleportEntity only moves the body.
		zm.inst.Players.Broadcast(CbPlayHeadRotation, zombieHeadRotationPayload(z.EntityID, yaw), -1)

		// Damage + attack animation once per second when in melee range.
		dy := s.Y - z.Y
		dist3DSq := dx*dx + dy*dy + dz*dz
		if dist3DSq < 2*2 && t%20 == 0 {
			zm.inst.Players.Broadcast(CbPlayEntityAnimation, zombieAttackAnimationPayload(z.EntityID), -1)
			_, newHP, _ := target.player.ApplyDamage(1.0, t, 10)
			_ = target.sendSetHealth(newHP)
		}
	}
}

// zombieTeleportPayload builds a Teleport Entity (0x68) payload for a zombie.
// Yaw is quantized to a single byte; pitch is always 0 (zombies don't look up).
func zombieTeleportPayload(z *Zombie, yaw float32) []byte {
	var payload []byte
	payload = append(payload, protocol.WriteVarInt32(z.EntityID)...)
	payload = append(payload, protocol.WriteDouble(z.X)...)
	payload = append(payload, protocol.WriteDouble(z.Y)...)
	payload = append(payload, protocol.WriteDouble(z.Z)...)
	payload = append(payload, protocol.AngleToByte(yaw))
	payload = append(payload, byte(0)) // pitch
	payload = append(payload, byte(1)) // on ground
	return payload
}

// zombieHeadRotationPayload builds a Head Rotation (0x42) packet for a zombie.
// Without this the zombie's head stays fixed regardless of body direction.
func zombieHeadRotationPayload(entityID int32, yaw float32) []byte {
	var payload []byte
	payload = append(payload, protocol.WriteVarInt32(entityID)...)
	payload = append(payload, protocol.AngleToByte(yaw))
	return payload
}

// zombieAttackAnimationPayload builds an Entity Animation (0x04) packet.
// Animation ID 0 = swing main hand, matching the vanilla melee attack visual.
func zombieAttackAnimationPayload(entityID int32) []byte {
	var payload []byte
	payload = append(payload, protocol.WriteVarInt32(entityID)...)
	payload = append(payload, byte(0)) // swing main hand
	return payload
}