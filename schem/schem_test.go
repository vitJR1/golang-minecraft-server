package schem

import (
	"bytes"
	"compress/gzip"
	"minecraft-server/nbt"
	"minecraft-server/world"
	"testing"
)

// varintEncode produces the Java-style VarInt encoding the Sponge
// BlockData field uses: 7 bits/byte, MSB = continuation.
func varintEncode(values []int32) []byte {
	var out []byte
	for _, v := range values {
		u := uint32(v)
		for {
			b := byte(u & 0x7F)
			u >>= 7
			if u != 0 {
				b |= 0x80
			}
			out = append(out, b)
			if u == 0 {
				break
			}
		}
	}
	return out
}

// buildSchemNBT crafts a Sponge v2 NBT payload with the given dimensions,
// palette, and (W*H*L) block indices. Returned bytes are raw NBT (no
// compression) — Parse auto-detects.
func buildSchemNBT(w, h, l int16, palette map[string]int32, blocks []int32) []byte {
	pal := nbt.Compound{}
	maxID := int32(0)
	for name, id := range palette {
		pal[name] = nbt.Int(id)
		if id > maxID {
			maxID = id
		}
	}
	root := nbt.Compound{
		"Version":    nbt.Int(2),
		"Width":      nbt.Short(w),
		"Height":     nbt.Short(h),
		"Length":     nbt.Short(l),
		"Palette":    pal,
		"PaletteMax": nbt.Int(maxID + 1),
		"BlockData":  nbt.ByteArray(varintEncode(blocks)),
	}
	return nbt.Marshal(root)
}

func TestParseBasic(t *testing.T) {
	// 2x1x2 cube: stone everywhere except (1,0,1) = air.
	// YZX order: y=0,z=0,x=0..1, y=0,z=1,x=0..1 = 4 blocks.
	data := buildSchemNBT(2, 1, 2,
		map[string]int32{
			"minecraft:air":   0,
			"minecraft:stone": 1,
		},
		[]int32{
			1, 1, // y=0, z=0
			1, 0, // y=0, z=1 (last block is air)
		},
	)
	s, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Width != 2 || s.Height != 1 || s.Length != 2 {
		t.Errorf("dims: got (%d,%d,%d), want (2,1,2)", s.Width, s.Height, s.Length)
	}
	if len(s.Blocks) != 4 {
		t.Errorf("Blocks length: got %d, want 4", len(s.Blocks))
	}
	if len(s.Palette) != 2 {
		t.Errorf("Palette length: got %d, want 2", len(s.Palette))
	}
}

func TestParseHandlesGzip(t *testing.T) {
	raw := buildSchemNBT(1, 1, 1,
		map[string]int32{"minecraft:stone": 0},
		[]int32{0},
	)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write(raw)
	_ = gz.Close()

	s, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatalf("Parse(gzip): %v", err)
	}
	if s.Width != 1 {
		t.Errorf("Width: got %d, want 1", s.Width)
	}
}

func TestToTemplatePlacesBlocks(t *testing.T) {
	// 3-block-long row of stone on the x-axis.
	data := buildSchemNBT(3, 1, 1,
		map[string]int32{"minecraft:stone": 0},
		[]int32{0, 0, 0},
	)
	s, _ := Parse(data)
	tmpl := s.ToTemplate()

	if got := tmpl.BlockCount(); got != 3 {
		t.Errorf("BlockCount: got %d, want 3", got)
	}
	w := tmpl.Instantiate()
	for x := 0; x < 3; x++ {
		if got := w.GetBlock(world.Position{X: x, Y: 0, Z: 0}); got != world.Stone {
			t.Errorf("(%d,0,0): got %+v, want Stone", x, got)
		}
	}
}

func TestToTemplateAtOffset(t *testing.T) {
	data := buildSchemNBT(1, 1, 1,
		map[string]int32{"minecraft:stone": 0},
		[]int32{0},
	)
	s, _ := Parse(data)
	tmpl := s.ToTemplateAt(100, 64, 200)
	w := tmpl.Instantiate()
	if got := w.GetBlock(world.Position{X: 100, Y: 64, Z: 200}); got != world.Stone {
		t.Errorf("offset placement: got %+v, want Stone at (100,64,200)", got)
	}
	if got := w.GetBlock(world.Position{X: 0, Y: 0, Z: 0}); got != world.Air {
		t.Errorf("origin should be empty: got %+v", got)
	}
}

func TestToTemplateUnknownBlockBecomesAir(t *testing.T) {
	data := buildSchemNBT(2, 1, 1,
		map[string]int32{
			"minecraft:stone":         0,
			"minecraft:made_up_block": 1,
		},
		[]int32{0, 1}, // (0,0,0) stone, (1,0,0) unknown
	)
	s, _ := Parse(data)
	w := s.ToTemplate().Instantiate()
	if got := w.GetBlock(world.Position{X: 0, Y: 0, Z: 0}); got != world.Stone {
		t.Errorf("(0,0,0): got %+v, want Stone", got)
	}
	if got := w.GetBlock(world.Position{X: 1, Y: 0, Z: 0}); got != world.Air {
		t.Errorf("unknown (1,0,0): got %+v, want Air", got)
	}
}

func TestStripProperties(t *testing.T) {
	cases := []struct{ in, want string }{
		{"minecraft:stone", "minecraft:stone"},
		{"minecraft:oak_stairs[facing=north]", "minecraft:oak_stairs"},
		{"minecraft:oak_stairs[facing=north,half=bottom]", "minecraft:oak_stairs"},
		{"[only_brackets]", ""},
	}
	for _, c := range cases {
		if got := stripProperties(c.in); got != c.want {
			t.Errorf("stripProperties(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPropertiesOnPaletteNamesStillResolveToBaseBlock(t *testing.T) {
	data := buildSchemNBT(1, 1, 1,
		map[string]int32{
			"minecraft:oak_planks[axis=y]": 0,
		},
		[]int32{0},
	)
	s, _ := Parse(data)
	w := s.ToTemplate().Instantiate()
	if got := w.GetBlock(world.Position{X: 0, Y: 0, Z: 0}); got != world.OakPlanks {
		t.Errorf("oak_planks with properties: got %+v, want OakPlanks", got)
	}
}

func TestParseRejectsMissingFields(t *testing.T) {
	bad := nbt.Marshal(nbt.Compound{
		"Version": nbt.Int(2),
		// no Width/Height/Length/Palette/BlockData
	})
	if _, err := Parse(bad); err == nil {
		t.Error("expected error for schematic missing dimensions")
	}
}

func TestVarIntDecoder(t *testing.T) {
	cases := [][]int32{
		{},
		{0},
		{1, 2, 3, 4, 5},
		{127, 128, 16383, 16384},
		{0, 1, 0, 2, 0, 3},
	}
	for _, c := range cases {
		encoded := varintEncode(c)
		decoded, err := decodeVarIntStream(encoded, len(c))
		if err != nil {
			t.Errorf("decode %v: %v", c, err)
			continue
		}
		if len(decoded) != len(c) {
			t.Errorf("length: got %d, want %d", len(decoded), len(c))
			continue
		}
		for i, v := range c {
			if decoded[i] != v {
				t.Errorf("index %d: got %d, want %d", i, decoded[i], v)
			}
		}
	}
}

func TestParsePalette(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantProp map[string]string
	}{
		{"minecraft:stone", "minecraft:stone", nil},
		{"minecraft:oak_stairs[facing=east,half=top]",
			"minecraft:oak_stairs",
			map[string]string{"facing": "east", "half": "top"}},
		{"minecraft:oak_slab[type=top,waterlogged=false]",
			"minecraft:oak_slab",
			map[string]string{"type": "top", "waterlogged": "false"}},
		{"minecraft:oak_log[axis=x]",
			"minecraft:oak_log",
			map[string]string{"axis": "x"}},
		{"minecraft:foo[]", "minecraft:foo", nil}, // empty bracket body
	}
	for _, c := range cases {
		gotName, gotProps := parsePalette(c.in)
		if gotName != c.wantName {
			t.Errorf("%q name: got %q, want %q", c.in, gotName, c.wantName)
		}
		if len(gotProps) != len(c.wantProp) {
			t.Errorf("%q props: got %v, want %v", c.in, gotProps, c.wantProp)
			continue
		}
		for k, v := range c.wantProp {
			if gotProps[k] != v {
				t.Errorf("%q[%s]: got %q, want %q", c.in, k, gotProps[k], v)
			}
		}
	}
}

func TestToTemplatePropertyAwareStairs(t *testing.T) {
	// 2 oak_stairs side by side: one facing east, one facing west.
	// Verify the StateID actually differs (i.e. props were honored).
	data := buildSchemNBT(2, 1, 1,
		map[string]int32{
			"minecraft:oak_stairs[facing=east,half=bottom,shape=straight,waterlogged=false]": 0,
			"minecraft:oak_stairs[facing=west,half=bottom,shape=straight,waterlogged=false]": 1,
		},
		[]int32{0, 1},
	)
	s, _ := Parse(data)
	w := s.ToTemplate().Instantiate()

	east := w.GetBlock(world.Position{X: 0, Y: 0, Z: 0})
	west := w.GetBlock(world.Position{X: 1, Y: 0, Z: 0})
	if east.Name != "minecraft:oak_stairs" {
		t.Fatalf("east block: got %+v", east)
	}
	if west.Name != "minecraft:oak_stairs" {
		t.Fatalf("west block: got %+v", west)
	}
	if east.StateID == west.StateID {
		t.Errorf("expected different StateIDs for east/west stairs, both got %d", east.StateID)
	}
	// Spot-check the actual values from the resolver (see world/states_test.go).
	if east.StateID != 2945 {
		t.Errorf("east stairs StateID: got %d, want 2945", east.StateID)
	}
}

func TestToTemplatePropertyAwareSlab(t *testing.T) {
	// Top and bottom oak_slab — should map to different state IDs.
	data := buildSchemNBT(2, 1, 1,
		map[string]int32{
			"minecraft:oak_slab[type=top,waterlogged=false]":    0,
			"minecraft:oak_slab[type=bottom,waterlogged=false]": 1,
		},
		[]int32{0, 1},
	)
	s, _ := Parse(data)
	w := s.ToTemplate().Instantiate()
	top := w.GetBlock(world.Position{X: 0, Y: 0, Z: 0})
	bot := w.GetBlock(world.Position{X: 1, Y: 0, Z: 0})
	if top.StateID == bot.StateID {
		t.Errorf("expected different StateIDs for top/bottom slab, both got %d", top.StateID)
	}
	if top.StateID != 11022 || bot.StateID != 11024 {
		t.Errorf("slab StateIDs: top=%d (want 11022) bot=%d (want 11024)",
			top.StateID, bot.StateID)
	}
}

func TestVarIntDecoderRejectsTruncated(t *testing.T) {
	// Continuation bit set on the last byte → incomplete VarInt.
	data := []byte{0x80}
	if _, err := decodeVarIntStream(data, 1); err == nil {
		t.Error("expected error on truncated VarInt")
	}
}
