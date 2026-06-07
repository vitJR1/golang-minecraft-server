package chunk

import (
	"minecraft-server/protocol"
)

// BuildEmptyChunkData builds an all-air column with every section's biome set
// to biomeID (a biome registry index, e.g. 39 = minecraft:plains).
func BuildEmptyChunkData(biomeID int32) []byte {
	data := make([]byte, 0, 24*16)

	for i := 0; i < 24; i++ {
		// Block count (Short)
		data = append(data, protocol.WriteShort(0)...)

		// ---- Block states paletted container ----
		// bitsPerEntry (Byte). 0 = single value
		data = append(data, 0)

		// Single value (VarInt) - 0 = air (в палитре/глобальном id это обычно 0)
		data = append(data, protocol.WriteVarInt32(0)...)

		// Data array length (VarInt) - 0 when bitsPerEntry == 0
		data = append(data, protocol.WriteVarInt32(0)...)

		// ---- Biomes paletted container ----
		data = append(data, 0)                                  // bitsPerEntry = 0
		data = append(data, protocol.WriteVarInt32(biomeID)...) // single biome value
		data = append(data, protocol.WriteVarInt32(0)...)       // data array length
	}

	return data
}
