package chunk

import (
	"bytes"
	"testing"
)

func TestEmptyChunkDataSize(t *testing.T) {
	// Each of 24 sections writes:
	//   Block count   (Short, 2 bytes)
	//   Block palette: bitsPerEntry (1 byte) + single-value VarInt (1 byte)
	//                + data array length VarInt (1 byte)   = 3 bytes
	//   Biome palette: same 3 bytes
	// Total per section: 2 + 3 + 3 = 8 bytes; 24 × 8 = 192.
	got := BuildEmptyChunkData(0)
	if len(got) != 24*8 {
		t.Errorf("empty chunk data: %d bytes, want %d", len(got), 24*8)
	}
}

func TestEmptyChunkDataSectionLayout(t *testing.T) {
	// Verify the first section's bytes match the documented layout: every
	// byte is zero (block count = 0, single-value palette = 0 = air, no data
	// array, biome palette = 0).
	data := BuildEmptyChunkData(0)
	for i := 0; i < 8; i++ {
		if data[i] != 0 {
			t.Errorf("section[0] byte[%d] = 0x%02x, want 0x00", i, data[i])
		}
	}
}

func TestEmptyHeightmapsIsRootCompound(t *testing.T) {
	b := BuildEmptyHeightmaps()
	// NBT root: TagCompound (0x0a) + empty name (uint16 0)
	if len(b) < 3 || b[0] != 0x0a || b[1] != 0x00 || b[2] != 0x00 {
		t.Fatalf("not a root compound: %x", b[:min(8, len(b))])
	}
}

func TestEmptyHeightmapsContainsBothKeys(t *testing.T) {
	b := BuildEmptyHeightmaps()
	for _, key := range []string{"MOTION_BLOCKING", "WORLD_SURFACE"} {
		if !bytes.Contains(b, []byte(key)) {
			t.Errorf("heightmaps NBT missing %q", key)
		}
	}
}

func TestEmptyHeightmapsLongArrayLengths(t *testing.T) {
	// Each LongArray is a 4-byte big-endian length prefix = 37 (0x00000025).
	b := BuildEmptyHeightmaps()
	if bytes.Count(b, []byte{0x00, 0x00, 0x00, 0x25}) < 2 {
		t.Errorf("expected two LongArray length prefixes (0x00000025); got %d", bytes.Count(b, []byte{0x00, 0x00, 0x00, 0x25}))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestChunkDataBiomeValue(t *testing.T) {
	// Section layout: [0,1]=block count, [2]=block bpe, [3]=block value,
	// [4]=block data len, [5]=biome bpe, [6]=biome value, [7]=biome data len.
	// A 1-byte biome id (39 = plains) lands at byte 6 of the first section.
	data := BuildEmptyChunkData(39)
	if data[6] != 39 {
		t.Errorf("biome value byte = 0x%02x, want 0x27 (39)", data[6])
	}
	// BuildChunkData carries the same biome for a real column.
	full := BuildChunkData(make([][]int32, SectionCount), 39)
	if full[6] != 39 {
		t.Errorf("BuildChunkData biome byte = 0x%02x, want 39", full[6])
	}
}
