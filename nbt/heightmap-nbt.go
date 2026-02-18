package nbt

func BuildHeightmapsNBT() []byte {
	w := New()
	w.WriteRootCompound()

	motion := packHeightmapConstant(64)
	surface := packHeightmapConstant(64)

	w.LongArray("MOTION_BLOCKING", motion)
	w.LongArray("WORLD_SURFACE", surface)

	w.EndCompound()
	return w.Bytes()
}

func packHeightmapConstant(h int64) []int64 {
	// Heightmaps use 9 bits per entry, 256 entries => 256*9 = 2304 bits => 36 longs.
	const (
		entries   = 256
		bits      = 9
		longCount = 36
	)
	out := make([]int64, longCount)

	var bitIndex int64 = 0
	for i := 0; i < entries; i++ {
		val := h & ((1 << bits) - 1)

		word := bitIndex / 64
		shift := bitIndex % 64

		out[word] |= val << shift
		if shift > 64-bits {
			out[word+1] |= val >> (64 - shift)
		}
		bitIndex += bits
	}
	return out
}
