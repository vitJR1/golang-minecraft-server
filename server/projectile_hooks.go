package server

import (
	"math"

	"minecraft-server/world"
)

// launchProjectile is game.Instance.ThrowProjectile: it spawns item as a
// projectile entity from thrower's eyes along their look direction at speed
// blocks/tick and lets the game drive it through onTick (false = stop) and
// onImpact. The flight itself (gravity, drag, collision) is projectileTick's.
// Unknown or non-entity items use the snowball entity so something is
// visible. Returns false when nothing was launched.
func (i *Instance) launchProjectile(thrower *ClientConnection, item string, speed float64,
	onTick func(x, y, z float64) bool, onImpact func(x, y, z float64)) bool {
	if thrower == nil || thrower.player == nil || i.Server == nil {
		return false
	}
	typeID, ok := throwableEntityID(item)
	if !ok {
		if id, known := world.EntityTypeID(item); known {
			typeID = id
		} else {
			typeID = snowballEntityID
		}
	}
	s := thrower.player.Snapshot()
	yaw := float64(s.Yaw) * math.Pi / 180
	pitch := float64(s.Pitch) * math.Pi / 180
	dx := -math.Sin(yaw) * math.Cos(pitch)
	dy := -math.Sin(pitch)
	dz := math.Cos(yaw) * math.Cos(pitch)

	p := &projectile{
		eid:      i.Server.nextEntityID.Add(1),
		typeID:   typeID,
		item:     item,
		x:        s.X,
		y:        s.Y + eyeHeight,
		z:        s.Z,
		vx:       dx * speed,
		vy:       dy * speed,
		vz:       dz * speed,
		thrower:  thrower,
		onTick:   onTick,
		onImpact: onImpact,
	}
	p.uuid = entityUUID(p.eid)

	i.projMu.Lock()
	i.projectiles = append(i.projectiles, p)
	i.projMu.Unlock()

	i.Players.Broadcast(CbPlaySpawnEntity, spawnProjectilePayload(p), -1)
	i.Players.Broadcast(CbPlayEntityVelocity, entityVelocityPayload(p.eid, p.vx, p.vy, p.vz), -1)
	return true
}
