package server

import (
	"minecraft-server/anticheat"
	"minecraft-server/server/gameplay/projectile"
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
	typeID, ok := projectile.ThrowableEntityID(item)
	if !ok {
		if id, known := world.EntityTypeID(item); known {
			typeID = id
		} else {
			typeID = projectile.SnowballEntityID
		}
	}
	s := thrower.player.Snapshot()
	look := anticheat.LookDir(s.Yaw, s.Pitch)
	eid := i.Server.nextEntityID.Add(1)
	i.addProjectile(&projectileEntity{
		Flight: projectile.Flight{
			EID: eid, UUID: entityUUID(eid), TypeID: typeID,
			X: s.X, Y: s.Y + eyeHeight, Z: s.Z,
			VX: look.X * speed, VY: look.Y * speed, VZ: look.Z * speed,
		},
		item:     item,
		thrower:  thrower,
		onTick:   onTick,
		onImpact: onImpact,
	})
	return true
}
