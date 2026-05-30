// Package schem parses Sponge Schematic v2 / v3 files into a
// world.Template that an instance can clone from.
//
// Workflow:
//
//	tmpl, err := schem.LoadFile("arenas/skywars.schem")
//	if err != nil { log.Fatal(err) }
//	srv.RegisterTemplate("skywars", tmpl)
//
// The .schem format is the de-facto schematic standard, produced by
// WorldEdit, FAWE, MCASelector, and Amulet. It carries a bounding box of
// blocks plus a palette (name → small integer); blocks are stored as a
// stream of VarInt indices into the palette.
//
// What we support:
//   - Sponge v2 (root-level fields) and v3 (fields under a "Schematic"
//     subcompound).
//   - gzip-compressed input (the default WorldEdit output).
//   - Block names with bracketed properties ("minecraft:oak_stairs[facing=north]").
//     Properties are stripped — we map only the base block.
//
// What we don't:
//   - Block entities (chests with items, signs with text).
//   - Entities.
//   - Properties (orientation, half, waterlogged, etc.). Blocks always
//     get their default state.
//   - Unknown block names → silently become Air.
package schem

import (
	"fmt"
	"os"
	"strings"

	"minecraft-server/nbt"
	"minecraft-server/world"
)

// Schematic is the parsed structure of a .schem file. Width/Height/Length
// are the bounding box; Palette is a flat slice keyed by the wire-level
// VarInt index; Blocks is W*H*L palette indices in YZX order (Y outermost,
// X innermost — per Sponge spec).
type Schematic struct {
	Version int32
	Width   int16
	Height  int16
	Length  int16
	Offset  [3]int32 // anchor offset; many tools leave it (0,0,0)
	Palette []string // index → namespaced name with optional [properties]
	Blocks  []int32  // length = Width*Height*Length
}

// LoadFile reads a .schem from disk and parses it.
func LoadFile(path string) (*Schematic, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes raw bytes (gzip-compressed or not) into a Schematic.
func Parse(data []byte) (*Schematic, error) {
	root, err := nbt.Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("nbt: %w", err)
	}

	// Sponge v3 nests everything under "Schematic"; v2 puts fields at the
	// root. Sniff and pick.
	inner := root
	if nested, ok := root["Schematic"].(nbt.Compound); ok {
		inner = nested
	}

	s := &Schematic{}
	if v, ok := inner["Version"].(nbt.Int); ok {
		s.Version = int32(v)
	}
	if w, ok := inner["Width"].(nbt.Short); ok {
		s.Width = int16(w)
	} else {
		return nil, fmt.Errorf("missing Width")
	}
	if h, ok := inner["Height"].(nbt.Short); ok {
		s.Height = int16(h)
	} else {
		return nil, fmt.Errorf("missing Height")
	}
	if l, ok := inner["Length"].(nbt.Short); ok {
		s.Length = int16(l)
	} else {
		return nil, fmt.Errorf("missing Length")
	}
	if off, ok := inner["Offset"].(nbt.IntArray); ok && len(off) == 3 {
		s.Offset = [3]int32{off[0], off[1], off[2]}
	}

	// Palette can live either at the top of `inner` (v2) or under
	// inner["Blocks"]["Palette"] (v3). Same for BlockData.
	palettePath, blockDataPath := inner, inner
	if blocks, ok := inner["Blocks"].(nbt.Compound); ok {
		palettePath, blockDataPath = blocks, blocks
	}

	palette, ok := palettePath["Palette"].(nbt.Compound)
	if !ok {
		return nil, fmt.Errorf("missing Palette")
	}
	// PaletteMax is optional; derive from entries if absent.
	paletteSize := int32(len(palette))
	if pm, ok := palettePath["PaletteMax"].(nbt.Int); ok && int32(pm) > paletteSize {
		paletteSize = int32(pm)
	}
	s.Palette = make([]string, paletteSize)
	for name, val := range palette {
		id, ok := val.(nbt.Int)
		if !ok {
			return nil, fmt.Errorf("palette entry %q is %T, want Int", name, val)
		}
		if int(id) < 0 {
			return nil, fmt.Errorf("palette entry %q has negative id %d", name, id)
		}
		if int(id) >= len(s.Palette) {
			extended := make([]string, int(id)+1)
			copy(extended, s.Palette)
			s.Palette = extended
		}
		s.Palette[id] = name
	}

	blockData, ok := blockDataPath["BlockData"].(nbt.ByteArray)
	if !ok {
		return nil, fmt.Errorf("missing BlockData")
	}
	expected := int(s.Width) * int(s.Height) * int(s.Length)
	s.Blocks, err = decodeVarIntStream(blockData, expected)
	if err != nil {
		return nil, fmt.Errorf("BlockData: %w", err)
	}
	if len(s.Blocks) != expected {
		return nil, fmt.Errorf("BlockData: decoded %d entries, want W*H*L=%d",
			len(s.Blocks), expected)
	}

	return s, nil
}

// decodeVarIntStream reads `expected` Java-style VarInts (7 bits/byte,
// MSB = continuation) from the byte slice. Errors if the stream is
// truncated or yields a different count than expected.
func decodeVarIntStream(data []byte, expected int) ([]int32, error) {
	out := make([]int32, 0, expected)
	var val uint32
	var shift uint
	for _, b := range data {
		val |= uint32(b&0x7F) << shift
		if b&0x80 == 0 {
			out = append(out, int32(val))
			val = 0
			shift = 0
			continue
		}
		shift += 7
		if shift >= 32 {
			return nil, fmt.Errorf("varint overruns 32 bits at byte %d", len(out))
		}
	}
	if shift != 0 {
		return nil, fmt.Errorf("trailing partial varint at end of stream")
	}
	return out, nil
}

// ToTemplate maps every non-Air block in the schematic into a fresh
// world.Template. Coordinates start at (0, 0, 0); call ToTemplateAt to
// shift the origin. Unknown block names (not in world.BlockByName) become
// Air silently — extend world/block.go's registry to recognize more.
func (s *Schematic) ToTemplate() *world.Template {
	return s.ToTemplateAt(0, 0, 0)
}

// ToTemplateAt places the schematic's (0,0,0) corner at world coordinate
// (originX, originY, originZ). Spawn points and other metadata aren't
// captured — add them on the returned template yourself.
func (s *Schematic) ToTemplateAt(originX, originY, originZ int) *world.Template {
	t := world.NewTemplate()
	idx := 0
	for y := 0; y < int(s.Height); y++ {
		for z := 0; z < int(s.Length); z++ {
			for x := 0; x < int(s.Width); x++ {
				paletteID := s.Blocks[idx]
				idx++
				if int(paletteID) >= len(s.Palette) {
					continue // bogus index, skip
				}
				name := stripProperties(s.Palette[paletteID])
				block, ok := world.BlockByName(name)
				if !ok || block == world.Air {
					continue
				}
				t.SetBlock(
					world.Position{
						X: originX + x,
						Y: originY + y,
						Z: originZ + z,
					},
					block,
				)
			}
		}
	}
	return t
}

// stripProperties drops the "[key=val,...]" suffix from a block name.
// "minecraft:oak_stairs[facing=north,half=bottom]" → "minecraft:oak_stairs"
func stripProperties(name string) string {
	if i := strings.Index(name, "["); i >= 0 {
		return name[:i]
	}
	return name
}

// LoadTemplate is the one-call convenience: read a file and produce a
// ready-to-register Template.
func LoadTemplate(path string) (*world.Template, error) {
	s, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	return s.ToTemplate(), nil
}
