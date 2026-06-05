package server

import "minecraft-server/protocol"

// Mob effect IDs (Minecraft Java 1.20.1).
// Reference: https://minecraft.wiki/w/Effect
const (
	EffectSpeed           = 1
	EffectSlowness        = 2
	EffectHaste           = 3
	EffectMiningFatigue   = 4
	EffectStrength        = 5
	EffectInstantHealth   = 6
	EffectInstantDamage   = 7
	EffectJumpBoost       = 8
	EffectNausea          = 9
	EffectRegeneration    = 10
	EffectResistance      = 11
	EffectFireResistance  = 12
	EffectWaterBreathing  = 13
	EffectInvisibility    = 14
	EffectBlindness       = 15
	EffectNightVision     = 16
	EffectHunger          = 17
	EffectWeakness        = 18
	EffectPoison          = 19
	EffectWither          = 20
	EffectHealthBoost     = 21
	EffectAbsorption      = 22
	EffectSaturation      = 23
	EffectGlowing         = 24
	EffectLevitation      = 25
	EffectLuck            = 26
	EffectBadLuck         = 27
)

// SendMobEffect отправляет эффект конкретному игроку.
//
//   - effectID:      константа Effect* выше
//   - amplifier:     0 = уровень I, 1 = уровень II и т.д.
//   - durationTicks: длительность в тиках (20 тиков = 1 секунда)
//   - hideParticles: true — скрыть визуальные частицы эффекта
func (c *ClientConnection) SendMobEffect(effectID int32, amplifier byte, durationTicks int32, hideParticles bool) error {
	var flags byte
	if hideParticles {
		flags |= 0x01
	}
	var payload []byte
	payload = append(payload, protocol.WriteVarInt32(int32(c.player.EntityID))...)
	payload = append(payload, protocol.WriteVarInt32(effectID)...)
	payload = append(payload, amplifier)
	payload = append(payload, protocol.WriteVarInt32(durationTicks)...)
	payload = append(payload, flags)
	payload = append(payload, 0x00) // Has Factor Data = false
	return c.safeWrite(CbPlayAddEffect, payload)
}
// RemoveMobEffect снимает эффект с игрока.
func (c *ClientConnection) RemoveMobEffect(effectID int32) error {
	var payload []byte
	payload = append(payload, protocol.WriteVarInt32(int32(c.player.EntityID))...)
	payload = append(payload, protocol.WriteVarInt32(effectID)...)
	return c.safeWrite(CbPlayRemoveEffect, payload)
}