package schem

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"

	"minecraft-server/nbt"
	"minecraft-server/world"
)

// write.go is the inverse of Parse: capture a region of a live world into a
// Schematic and serialise it as a Sponge v2 .schem (gzip-compressed NBT),
// the layout WorldEdit and the loader in this package both accept. Block
// states round-trip through world.PaletteName, so stairs/slabs/logs keep
// their properties; block-entity markers (beds, chests, …) are kept so the
// blocks render when the file is loaded again. Entities and biomes aren't
// written — the reader treats both as optional.

// FromWorld captures every block inside the inclusive box [min, max] of w
// (corners may be given in any order). Air inside the box is stored as air,
// so a paste overwrites what was there — like WorldEdit's default. The
// returned schematic's (0,0,0) is the box's minimum corner.
func FromWorld(w world.World, a, b world.Position) *Schematic {
	lo, hi := minCorner(a, b), maxCorner(a, b)
	width, height, length := hi.X-lo.X+1, hi.Y-lo.Y+1, hi.Z-lo.Z+1

	s := &Schematic{
		Version:       2,
		Width:         int16(width),
		Height:        int16(height),
		Length:        int16(length),
		Blocks:        make([]int32, width*height*length),
		BlockEntities: map[world.Position]string{},
	}
	paletteIdx := map[string]int32{}
	intern := func(name string) int32 {
		if id, ok := paletteIdx[name]; ok {
			return id
		}
		id := int32(len(s.Palette))
		paletteIdx[name] = id
		s.Palette = append(s.Palette, name)
		return id
	}
	intern("minecraft:air") // index 0 = air, so the zero value of Blocks is air

	idx := 0
	for y := 0; y < height; y++ {
		for z := 0; z < length; z++ {
			for x := 0; x < width; x++ {
				blk := w.GetBlock(world.Position{X: lo.X + x, Y: lo.Y + y, Z: lo.Z + z})
				if blk != world.Air && blk.Name != "" {
					s.Blocks[idx] = intern(world.PaletteName(blk))
				}
				idx++
			}
		}
	}

	if bep, ok := w.(world.BlockEntityProvider); ok {
		for p, typ := range bep.BlockEntities() {
			if p.X < lo.X || p.X > hi.X || p.Y < lo.Y || p.Y > hi.Y || p.Z < lo.Z || p.Z > hi.Z {
				continue
			}
			s.BlockEntities[world.Position{X: p.X - lo.X, Y: p.Y - lo.Y, Z: p.Z - lo.Z}] = typ
		}
	}
	return s
}

// Marshal serialises the schematic as gzip-compressed Sponge v2 NBT — the
// bytes a .schem file holds. Parse(s.Marshal()) yields an equivalent
// Schematic.
func (s *Schematic) Marshal() ([]byte, error) {
	if len(s.Blocks) != int(s.Width)*int(s.Height)*int(s.Length) {
		return nil, fmt.Errorf("schem: %d blocks, want W*H*L=%d",
			len(s.Blocks), int(s.Width)*int(s.Height)*int(s.Length))
	}

	palette := nbt.Compound{}
	for i, name := range s.Palette {
		if name == "" {
			continue
		}
		palette[name] = nbt.Int(int32(i))
	}

	var data bytes.Buffer
	for _, id := range s.Blocks {
		writeVarInt(&data, id)
	}

	root := nbt.Compound{
		"Version":     nbt.Int(2),
		"DataVersion": nbt.Int(dataVersion1_20_1),
		"Width":       nbt.Short(s.Width),
		"Height":      nbt.Short(s.Height),
		"Length":      nbt.Short(s.Length),
		"Offset":      nbt.IntArray{s.Offset[0], s.Offset[1], s.Offset[2]},
		"PaletteMax":  nbt.Int(int32(len(s.Palette))),
		"Palette":     palette,
		"BlockData":   nbt.ByteArray(data.Bytes()),
	}
	if len(s.BlockEntities) > 0 {
		list := nbt.List{ElemTag: nbt.TagCompound}
		for p, typ := range s.BlockEntities {
			list.Items = append(list.Items, nbt.Compound{
				"Id":  nbt.String(typ),
				"Pos": nbt.IntArray{int32(p.X), int32(p.Y), int32(p.Z)},
			})
		}
		root["BlockEntities"] = list
	}

	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	if _, err := gz.Write(nbt.Marshal(root)); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	return out.Bytes(), nil
}

// SaveFile writes the schematic to path, creating parent directories.
func SaveFile(path string, s *Schematic) error {
	data, err := s.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// dataVersion1_20_1 is the vanilla data version stamped into written files;
// informational only (the reader ignores it).
const dataVersion1_20_1 = 3465

// writeVarInt appends v in the same 7-bit-continuation encoding
// decodeVarIntStream reads.
func writeVarInt(buf *bytes.Buffer, v int32) {
	u := uint32(v)
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		buf.WriteByte(b)
		if u == 0 {
			return
		}
	}
}

func minCorner(a, b world.Position) world.Position {
	return world.Position{X: min(a.X, b.X), Y: min(a.Y, b.Y), Z: min(a.Z, b.Z)}
}

func maxCorner(a, b world.Position) world.Position {
	return world.Position{X: max(a.X, b.X), Y: max(a.Y, b.Y), Z: max(a.Z, b.Z)}
}
