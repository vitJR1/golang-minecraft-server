package server

import "encoding/hex"

// Vanilla / wire values the effect, attribute, absorption and hand-state
// tests read packets with (minecraft-data 1.20, vanilla 1.20.1), kept apart
// from the packages' own so the tests pin the protocol.
const (
	playerMetaAbsorption byte = 15 // Player "Additional Hearts"
	handStateIndex       byte = 8  // LivingEntity hand states

	movementSpeedAttribute   = "minecraft:generic.movement_speed"
	playerBaseMovementSpeed  = 0.1
	attributeOpMultiplyTotal = 2 // value *= 1 + amount
)

var (
	speedModifierUUID    = mustHex("91aeaa56376b4498935b2f7f68070635")
	slownessModifierUUID = mustHex("7107de5e7ce84030940e514c1f160890")
	sprintModifierUUID   = mustHex("662a6b8dda3e4c1c881396ea6097278d")
	sprintBoost          = float64(float32(0.3)) // a float32 0.3 widened, like the Java client
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
