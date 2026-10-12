package server

import (
	"bytes"

	"minecraft-server/protocol"
)

// attributes.go: the generic.movement_speed attribute.
//
// The 1.20.1 client never applies Speed / Slowness itself: an Entity Effect
// packet only draws the icon and the particles, while the actual speed
// change is an attribute modifier that vanilla's server adds to the player
// and syncs through Update Attributes (0x6A). The client also derives its
// field of view from that attribute, so the Speed zoom-out comes with it.
//
// The sprint boost is included while the player sprints: the client drops
// every modifier it holds for an attribute when the packet arrives and
// would otherwise lose its own sprint boost until the next sprint.

const (
	movementSpeedAttribute   = "minecraft:generic.movement_speed"
	playerBaseMovementSpeed  = 0.1
	attributeOpMultiplyTotal = 2 // value *= 1 + amount

	speedPerLevel    = 0.2   // vanilla MobEffects.MOVEMENT_SPEED: 0.2 × (amplifier+1)
	slownessPerLevel = -0.15 // vanilla MobEffects.MOVEMENT_SLOWDOWN: -0.15 × (amplifier+1)
)

// sprintBoost is vanilla's "Sprinting speed boost" amount, a float32 0.3
// widened to double exactly as the Java client does.
var sprintBoost = float64(float32(0.3))

// Modifier UUIDs, from vanilla; the client replaces modifiers by UUID.
var (
	speedModifierUUID    = mustUUID("91AEAA56-376B-4498-935B-2F7F68070635")
	slownessModifierUUID = mustUUID("7107DE5E-7CE8-4030-940E-514C1F160890")
	sprintModifierUUID   = mustUUID("662A6B8D-DA3E-4C1C-8813-96EA6097278D")
)

func mustUUID(s string) []byte {
	raw, err := protocol.WriteUUID(s)
	if err != nil {
		panic("attributes: bad modifier uuid " + s)
	}
	return raw
}

// attributeModifier is one entry of an Update Attributes property.
type attributeModifier struct {
	uuid   []byte
	amount float64
	op     byte
}

// movementSpeedModifiers builds the modifier list for a player with Speed
// at speedLvl, Slowness at slowLvl (0 = none) who is or isn't sprinting.
func movementSpeedModifiers(speedLvl, slowLvl int, sprinting bool) []attributeModifier {
	var mods []attributeModifier
	if speedLvl > 0 {
		mods = append(mods, attributeModifier{speedModifierUUID, speedPerLevel * float64(speedLvl), attributeOpMultiplyTotal})
	}
	if slowLvl > 0 {
		mods = append(mods, attributeModifier{slownessModifierUUID, slownessPerLevel * float64(slowLvl), attributeOpMultiplyTotal})
	}
	if sprinting {
		mods = append(mods, attributeModifier{sprintModifierUUID, sprintBoost, attributeOpMultiplyTotal})
	}
	return mods
}

// movementSpeedAttributePayload builds Update Attributes (0x6A) with the
// single generic.movement_speed property: base 0.1 plus the modifiers.
func movementSpeedAttributePayload(entityID int32, mods []attributeModifier) []byte {
	var buf bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&buf, entityID)
	protocol.WriteVarInt32ToBuffer(&buf, 1) // property count
	buf.Write(protocol.WriteString(movementSpeedAttribute))
	buf.Write(protocol.WriteDouble(playerBaseMovementSpeed))
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(mods)))
	for _, m := range mods {
		buf.Write(m.uuid)
		buf.Write(protocol.WriteDouble(m.amount))
		buf.WriteByte(m.op)
	}
	return buf.Bytes()
}

// affectsMovementSpeed reports whether e changes the movement_speed
// attribute and so needs an attribute sync when it comes or goes.
func affectsMovementSpeed(e Effect) bool {
	return e == EffectSpeed || e == EffectSlowness
}

// sendMovementAttributes syncs the player's movement speed (Speed /
// Slowness levels, sprint state) to their client. Called whenever either
// effect is applied or ends, and after every Respawn, which resets the
// client's attributes.
func (c *ClientConnection) sendMovementAttributes() error {
	if c.player == nil {
		return nil
	}
	mods := movementSpeedModifiers(c.effectLevel(EffectSpeed), c.effectLevel(EffectSlowness), c.sprinting.Load())
	return c.safeWrite(CbPlayUpdateAttributes, movementSpeedAttributePayload(c.player.EntityID, mods))
}
