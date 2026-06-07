package chunk

import (
	"math/bits"

	"minecraft-server/protocol"
)

// SectionCount is the number of 16-tall block sections in a 1.20.1 Overworld
// column (min_y=-64, height=384 → 24 sections).
const SectionCount = 24

// MinY is the lowest block Y in the Overworld dimension. World Y maps to a
// section index via (y - MinY) / 16.
const MinY = -64

// blocksPerSection is 16×16×16.
const blocksPerSection = 4096

// directBits is the bits-per-entry used when a section has too many distinct
// block states for an indirect palette (>256). It must address the whole
// global block-state registry; 15 bits covers 1.20.1's ~27k states.
const directBits = 15

// airStateID is the global state ID for minecraft:air (palette/section
// default). Blocks left unset in a section are air.
const airStateID int32 = 0

// BuildChunkData serialises the block-state + biome paletted containers for
// a full column, mirroring BuildEmptyChunkData's structure but with real
// blocks. sections has one entry per vertical section (index 0 = lowest, at
// MinY); a nil entry is an all-air section. A non-nil entry is exactly
// blocksPerSection state IDs in YZX order (index = y*256 + z*16 + x, all
// local 0..15). Extra/short slices are tolerated (missing cells = air).
//
// Biomes are emitted as a single-valued container (biomeID) per section — one
// biome for the whole column, which covers uniform maps. biomeID is the biome's
// registry index (see server.BiomeID), e.g. 39 = minecraft:plains.
func BuildChunkData(sections [][]int32, biomeID int32) []byte {
	data := make([]byte, 0, SectionCount*64)
	for i := 0; i < SectionCount; i++ {
		var states []int32
		if i < len(sections) {
			states = sections[i]
		}
		data = appendBlockStates(data, states)
		data = appendSingleBiome(data, biomeID)
	}
	return data
}

// appendBlockStates writes one section's block-state paletted container:
// non-air block count (Short), then bitsPerEntry + palette + packed data.
func appendBlockStates(buf []byte, states []int32) []byte {
	// Build the palette (distinct states in first-seen order) and count
	// non-air blocks for the section header.
	index := make(map[int32]int, 16)
	palette := make([]int32, 0, 16)
	nonAir := 0
	at := func(i int) int32 {
		if i < len(states) {
			return states[i]
		}
		return airStateID
	}
	for i := 0; i < blocksPerSection; i++ {
		s := at(i)
		if s != airStateID {
			nonAir++
		}
		if _, ok := index[s]; !ok {
			index[s] = len(palette)
			palette = append(palette, s)
		}
	}

	buf = append(buf, protocol.WriteShort(int16(nonAir))...)

	// Single-valued: the whole section is one block state (commonly air).
	if len(palette) == 1 {
		buf = append(buf, 0) // bitsPerEntry = 0
		buf = append(buf, protocol.WriteVarInt32(palette[0])...)
		buf = append(buf, protocol.WriteVarInt32(0)...) // data array length
		return buf
	}

	// Indirect palette for ≤256 distinct states (4..8 bits), else direct.
	bpe := bitsFor(len(palette))
	if bpe < 4 {
		bpe = 4
	}
	if bpe > 8 {
		return appendDirect(buf, states)
	}

	buf = append(buf, byte(bpe))
	buf = append(buf, protocol.WriteVarInt32(int32(len(palette)))...)
	for _, s := range palette {
		buf = append(buf, protocol.WriteVarInt32(s)...)
	}
	longs := packIndices(states, bpe, func(s int32) int32 { return int32(index[s]) })
	buf = append(buf, protocol.WriteVarInt32(int32(len(longs)))...)
	for _, l := range longs {
		buf = append(buf, protocol.WriteLong(int64(l))...)
	}
	return buf
}

// appendDirect writes a section using the global palette (no per-section
// palette): each entry is the block's own state ID, directBits wide.
func appendDirect(buf []byte, states []int32) []byte {
	buf = append(buf, byte(directBits))
	longs := packIndices(states, directBits, func(s int32) int32 { return s })
	buf = append(buf, protocol.WriteVarInt32(int32(len(longs)))...)
	for _, l := range longs {
		buf = append(buf, protocol.WriteLong(int64(l))...)
	}
	return buf
}

// appendSingleBiome writes a single-valued biome container set to biomeID
// (a biome registry index). bitsPerEntry 0 = the whole section is one biome.
func appendSingleBiome(buf []byte, biomeID int32) []byte {
	buf = append(buf, 0)                                  // bitsPerEntry = 0
	buf = append(buf, protocol.WriteVarInt32(biomeID)...) // single biome value
	buf = append(buf, protocol.WriteVarInt32(0)...)       // data array length
	return buf
}

// packIndices packs blocksPerSection entries into 64-bit longs at `bpe` bits
// each, using the 1.16+ non-spanning layout (entries never cross a long
// boundary; high bits of each long are padding). value maps a state ID to
// the integer to store (palette index for indirect, state ID for direct).
func packIndices(states []int32, bpe int, value func(int32) int32) []uint64 {
	per := 64 / bpe
	n := (blocksPerSection + per - 1) / per
	out := make([]uint64, n)
	mask := uint64(1)<<uint(bpe) - 1
	for i := 0; i < blocksPerSection; i++ {
		s := airStateID
		if i < len(states) {
			s = states[i]
		}
		v := uint64(value(s)) & mask
		li := i / per
		off := uint((i % per) * bpe)
		out[li] |= v << off
	}
	return out
}

// bitsFor returns the number of bits needed to represent indices 0..n-1.
func bitsFor(n int) int {
	if n <= 1 {
		return 0
	}
	return bits.Len(uint(n - 1))
}
