package server

import (
	"math"
	"math/rand"
	"minecraft-server/protocol"
)

// Entity type IDs (Minecraft Java 1.20.1).
const (
	EntityTypeZombie   = 118 // minecraft:zombie (1.20.1)
)

// SpawnMob спавнит моба в указанных координатах для данного игрока.
// Возвращает entityID заспавненного моба.
//
// Использует пакет Spawn Entity (0x01) — в 1.20.1 все сущности,
// включая мобов, спавнятся через него.
func (c *ClientConnection) SpawnMob(entityType int32, x, y, z float64) (int32, error) {
	entityID := c.server.AllocEntityID()
	uuid := protocol.RandomUUID()

	var payload []byte
	payload = append(payload, protocol.WriteVarInt32(entityID)...)   // Entity ID
	payload = append(payload, uuid[:]...)                             // UUID (16 bytes)
	payload = append(payload, protocol.WriteVarInt32(entityType)...)  // Entity Type
	payload = append(payload, protocol.WriteDouble(x)...)             // X
	payload = append(payload, protocol.WriteDouble(y)...)             // Y
	payload = append(payload, protocol.WriteDouble(z)...)             // Z
	payload = append(payload, byte(0))                                // Pitch
	payload = append(payload, byte(0))                                // Yaw
	payload = append(payload, byte(0))                                // Head Yaw
	payload = append(payload, protocol.WriteVarInt32(0)...)           // Data
	payload = append(payload, protocol.WriteShort(0)...)              // Velocity X
	payload = append(payload, protocol.WriteShort(0)...)              // Velocity Y
	payload = append(payload, protocol.WriteShort(0)...)              // Velocity Z

	return entityID, c.safeWrite(CbPlaySpawnEntity, payload)
}

// SpawnZombieGroup спавнит count зомби вокруг точки (x, y, z)
// в радиусе radius блоков — равномерно по кругу.
func (c *ClientConnection) SpawnZombieGroup(x, y, z float64, count int, radius float64) ([]int32, error) {
	ids := make([]int32, 0, count)
	for i := 0; i < count; i++ {
		angle := rand.Float64() * 2 * math.Pi
		dist := rand.Float64() * radius
		zx := x + math.Cos(angle)*dist
		zz := z + math.Sin(angle)*dist
		id, err := c.SpawnMob(EntityTypeZombie, zx, y, zz)
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}