package chunk

import "minecraft-server/nbt"

// BuildEmptyHeightmaps returns the heightmaps NBT compound for a fully-empty
// chunk in 1.20.1 format.
//
// Required entries: MOTION_BLOCKING and WORLD_SURFACE. Each is a LongArray
// holding 256 packed 9-bit values (one per X-Z column). 1.18+ uses
// non-spanning packing — floor(64/9) = 7 entries per long, so
// ceil(256/7) = 37 longs per map. For an empty chunk every value is 0.
func BuildEmptyHeightmaps() []byte {
	const longsPerMap = 37
	return nbt.Marshal(nbt.Compound{
		"MOTION_BLOCKING": make(nbt.LongArray, longsPerMap),
		"WORLD_SURFACE":   make(nbt.LongArray, longsPerMap),
	})
}
