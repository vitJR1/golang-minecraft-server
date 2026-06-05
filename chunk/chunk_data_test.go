package chunk

import (
	"bytes"
	"testing"
)

// readVarInt reads a Java VarInt from b, returning the value and the number
// of bytes consumed.
func readVarInt(b []byte) (int32, int) {
	var val uint32
	var shift, n int
	for {
		c := b[n]
		n++
		val |= uint32(c&0x7F) << shift
		if c&0x80 == 0 {
			break
		}
		shift += 7
	}
	return int32(val), n
}

// section parses one block-state container from the front of b (skipping the
// leading block-count short) and returns its decoded 4096 state IDs plus the
// number of bytes the whole section (block states + biomes) consumed.
func decodeSection(t *testing.T, b []byte) (states [blocksPerSection]int32, n int) {
	t.Helper()
	count := int16(b[0])<<8 | int16(b[1])
	_ = count
	n = 2
	bpe := int(b[n])
	n++

	switch {
	case bpe == 0:
		val, k := readVarInt(b[n:])
		n += k
		_, k = readVarInt(b[n:]) // data len (0)
		n += k
		for i := range states {
			states[i] = val
		}
	default:
		// palette (indirect) — direct (bpe>=9) has no palette
		var palette []int32
		if bpe <= 8 {
			plen, k := readVarInt(b[n:])
			n += k
			palette = make([]int32, plen)
			for i := range palette {
				palette[i], k = readVarInt(b[n:])
				n += k
			}
		}
		longCount, k := readVarInt(b[n:])
		n += k
		longs := make([]uint64, longCount)
		for i := range longs {
			longs[i] = bytesToU64(b[n:])
			n += 8
		}
		per := 64 / bpe
		mask := uint64(1)<<uint(bpe) - 1
		for i := range states {
			v := (longs[i/per] >> uint((i%per)*bpe)) & mask
			if palette != nil {
				states[i] = palette[v]
			} else {
				states[i] = int32(v)
			}
		}
	}

	// Skip the biome container: bpe(0) + value varint + len varint.
	bn := n
	bn++ // biome bpe = 0
	_, k := readVarInt(b[bn:])
	bn += k
	_, k = readVarInt(b[bn:])
	bn += k
	return states, bn
}

func bytesToU64(b []byte) uint64 {
	var v uint64
	for i := 0; i < 8; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v
}

func setBlock(sections [][]int32, lx, ly, lz int, state int32) {
	sec := ly / 16
	if sections[sec] == nil {
		sections[sec] = make([]int32, blocksPerSection)
	}
	yy := ly % 16
	sections[sec][yy*256+lz*16+lx] = state
}

func TestBuildChunkDataRoundTrip(t *testing.T) {
	sections := make([][]int32, SectionCount)
	// A handful of distinct blocks (forces an indirect palette) in section 0.
	want := map[[3]int]int32{
		{0, 0, 0}:   1,    // stone
		{1, 0, 0}:   2047, // white_wool
		{15, 5, 15}: 1915, // red_bed
		{8, 10, 3}:  2058, // blue_wool
		{2, 0, 14}:  79,   // bedrock
	}
	for p, s := range want {
		setBlock(sections, p[0], p[1], p[2], s)
	}

	data := BuildChunkData(sections)

	// Decode section 0 and verify the planted blocks survive the round-trip.
	states, _ := decodeSection(t, data)
	for p, s := range want {
		idx := (p[1]%16)*256 + p[2]*16 + p[0]
		if states[idx] != s {
			t.Errorf("block %v: got state %d, want %d", p, states[idx], s)
		}
	}
	// Everything else in section 0 is air.
	planted := map[int]bool{}
	for p := range want {
		planted[(p[1]%16)*256+p[2]*16+p[0]] = true
	}
	for i, s := range states {
		if !planted[i] && s != 0 {
			t.Errorf("index %d: got %d, want air", i, s)
		}
	}
}

func TestAllAirSectionsAreSingleValued(t *testing.T) {
	empty := BuildChunkData(make([][]int32, SectionCount))
	// An all-air chunk must be byte-identical to BuildEmptyChunkData: every
	// section single-valued air. This guarantees we didn't regress the empty
	// path that the hub relies on.
	if !bytes.Equal(empty, BuildEmptyChunkData()) {
		t.Error("all-air BuildChunkData differs from BuildEmptyChunkData")
	}
}

func TestBitsFor(t *testing.T) {
	cases := map[int]int{1: 0, 2: 1, 3: 2, 4: 2, 5: 3, 16: 4, 17: 5, 256: 8, 257: 9}
	for n, want := range cases {
		if got := bitsFor(n); got != want {
			t.Errorf("bitsFor(%d): got %d, want %d", n, got, want)
		}
	}
}

func TestDirectPaletteForManyStates(t *testing.T) {
	// >256 distinct states in one section forces the direct (global) palette.
	sections := make([][]int32, SectionCount)
	sections[0] = make([]int32, blocksPerSection)
	for i := 0; i < blocksPerSection; i++ {
		sections[0][i] = int32(i%300 + 1) // 300 distinct non-air states
	}
	data := BuildChunkData(sections)
	states, _ := decodeSection(t, data)
	for i := 0; i < blocksPerSection; i++ {
		if want := int32(i%300 + 1); states[i] != want {
			t.Fatalf("index %d: got %d, want %d (direct palette)", i, states[i], want)
		}
	}
}
