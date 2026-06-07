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
// What we support (cont.):
//   - Block properties (facing/half/type/axis/connections) via
//     world.ResolveStateID for blocks with a variant table.
//   - Item-frame entities (minecraft:item_frame / glow_item_frame) from the
//     "Entities" list, with facing, item rotation, and displayed item.
//
// What we don't:
//   - Block entities (chests with items, signs with text, banner patterns).
//   - Entity kinds other than item frames (paintings, armor stands, …).
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
	Version  int32
	Width    int16
	Height   int16
	Length   int16
	Offset   [3]int32       // anchor offset; many tools leave it (0,0,0)
	Palette  []string       // index → namespaced name with optional [properties]
	Blocks   []int32        // length = Width*Height*Length
	Entities []world.Entity // non-block objects (item frames today)

	// BlockEntities are block-entity markers (bed/chest/banner/skull/…) keyed
	// by schematic-local position → block-entity type name. The client needs
	// these to render BlockEntityRenderer blocks.
	BlockEntities map[world.Position]string

	// Biome is the schematic's dominant biome name ("minecraft:plains"), or ""
	// if none is stored. We model a single biome per map (uniform), which
	// covers typical hand-built arenas.
	Biome string
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

	// Entities (item frames etc.) live at inner["Entities"] in v3. Optional —
	// most schematics have none. Unknown entity kinds are skipped.
	if ents, ok := inner["Entities"].(nbt.List); ok {
		s.Entities = parseEntities(ents)
	}

	// Block entities live at inner["BlockEntities"] (v3) or under
	// inner["Blocks"]["BlockEntities"]. Optional.
	bePath := inner
	if blocks, ok := inner["Blocks"].(nbt.Compound); ok {
		bePath = blocks
	}
	if bes, ok := bePath["BlockEntities"].(nbt.List); ok {
		s.BlockEntities = parseBlockEntities(bes)
	}

	s.Biome = parseDominantBiome(inner)

	return s, nil
}

// parseDominantBiome returns the most common biome name in the schematic, or
// "" if biomes aren't stored. Handles both the v2 flat layout (BiomePalette +
// BiomeData) and the v3 nested one (Biomes{Palette,Data}). BiomeData is a
// VarInt stream of palette indices (2D per-column in v2); we only need the
// dominant entry since we model one biome per map.
func parseDominantBiome(inner nbt.Compound) string {
	palette, _ := inner["BiomePalette"].(nbt.Compound)
	data, _ := inner["BiomeData"].(nbt.ByteArray)
	if biomes, ok := inner["Biomes"].(nbt.Compound); ok {
		if p, ok := biomes["Palette"].(nbt.Compound); ok {
			palette = p
		}
		if d, ok := biomes["Data"].(nbt.ByteArray); ok {
			data = d
		}
	}
	if len(palette) == 0 {
		return ""
	}

	// index → biome name
	names := map[int32]string{}
	for name, v := range palette {
		if id, ok := v.(nbt.Int); ok {
			names[int32(id)] = name
		}
	}
	// Single-entry palette: that's the biome, no need to scan the data.
	if len(palette) == 1 {
		for _, name := range names {
			return name
		}
	}

	// Tally palette indices across the data stream and pick the most common.
	counts := map[int32]int{}
	var val uint32
	var shift uint
	for _, b := range data {
		val |= uint32(b&0x7F) << shift
		if b&0x80 == 0 {
			counts[int32(val)]++
			val, shift = 0, 0
			continue
		}
		shift += 7
		if shift >= 32 {
			return "" // malformed
		}
	}
	best, bestN := int32(0), -1
	for id, n := range counts {
		if n > bestN {
			best, bestN = id, n
		}
	}
	return names[best]
}

// parseBlockEntities reads the Sponge "BlockEntities" list into a
// position → type-name map. Each entry has "Id" (type) + "Pos" (3-int array,
// schematic-local). Entries without a usable id/pos are skipped.
func parseBlockEntities(list nbt.List) map[world.Position]string {
	out := make(map[world.Position]string)
	for _, item := range list.Items {
		c, ok := item.(nbt.Compound)
		if !ok {
			continue
		}
		id := nbtString(c, "Id")
		if id == "" {
			id = nbtString(c, "id")
		}
		if id == "" {
			continue
		}
		x, y, z, ok := nbtIntPos(c)
		if !ok {
			continue
		}
		out[world.Position{X: x, Y: y, Z: z}] = id
	}
	return out
}

// nbtIntPos reads a 3-element integer "Pos" (TAG_Int_Array, or a List of Int).
func nbtIntPos(c nbt.Compound) (x, y, z int, ok bool) {
	if arr, ok := c["Pos"].(nbt.IntArray); ok && len(arr) == 3 {
		return int(arr[0]), int(arr[1]), int(arr[2]), true
	}
	if lst, ok := c["Pos"].(nbt.List); ok && len(lst.Items) == 3 {
		gi := func(v nbt.Value) int {
			if iv, ok := v.(nbt.Int); ok {
				return int(iv)
			}
			return 0
		}
		return gi(lst.Items[0]), gi(lst.Items[1]), gi(lst.Items[2]), true
	}
	return 0, 0, 0, false
}

// parseEntities extracts the entity kinds we render (item frames) from a
// Sponge "Entities" list. Each entry is a Compound with "Id" + "Pos"; the
// entity-specific NBT (Facing/ItemRotation/Item) sits either at the top level
// or under a "Data" sub-compound depending on the exporter, so we look in
// both. Unrecognized entity ids are skipped.
func parseEntities(list nbt.List) []world.Entity {
	var out []world.Entity
	for _, item := range list.Items {
		c, ok := item.(nbt.Compound)
		if !ok {
			continue
		}
		id := nbtString(c, "Id")
		if id == "" {
			id = nbtString(c, "id")
		}
		glow := id == "minecraft:glow_item_frame"
		if id != "minecraft:item_frame" && !glow {
			continue // only item frames are modelled today
		}

		x, y, z, ok := nbtPos(c)
		if !ok {
			continue
		}
		// Frame fields may be flattened into c or nested under "Data".
		data := c
		if d, ok := c["Data"].(nbt.Compound); ok {
			data = d
		}
		frame := &world.FrameData{
			Facing:   byte(nbtByte(data, "Facing")),
			Rotation: byte(nbtByte(data, "ItemRotation")),
			Item:     frameItem(data),
			Glowing:  glow,
		}
		out = append(out, world.Entity{Type: id, X: x, Y: y, Z: z, Frame: frame})
	}
	return out
}

func nbtString(c nbt.Compound, key string) string {
	if v, ok := c[key].(nbt.String); ok {
		return string(v)
	}
	return ""
}

func nbtByte(c nbt.Compound, key string) int8 {
	if v, ok := c[key].(nbt.Byte); ok {
		return int8(v)
	}
	return 0
}

// nbtPos reads a 3-element Double "Pos" list.
func nbtPos(c nbt.Compound) (x, y, z float64, ok bool) {
	lst, ok := c["Pos"].(nbt.List)
	if !ok || len(lst.Items) != 3 {
		return 0, 0, 0, false
	}
	d := func(v nbt.Value) float64 {
		if dv, ok := v.(nbt.Double); ok {
			return float64(dv)
		}
		return 0
	}
	return d(lst.Items[0]), d(lst.Items[1]), d(lst.Items[2]), true
}

// frameItem reads the displayed item's namespaced id from an item frame's
// "Item" compound ({id, Count}). Returns "" for an empty frame.
func frameItem(c nbt.Compound) string {
	item, ok := c["Item"].(nbt.Compound)
	if !ok {
		return ""
	}
	return nbtString(item, "id")
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
				name, props := parsePalette(s.Palette[paletteID])
				block, ok := world.BlockByName(name)
				if !ok || block == world.Air {
					continue
				}
				// Property-aware: stairs facing, slab half, log axis etc.
				// Falls back to block's default StateID when the block
				// has no variants registered or props is empty.
				block.StateID = world.ResolveStateID(name, props)
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
	// Entities use schematic-local positions; shift by the same origin as
	// blocks (the schematic Offset is intentionally ignored, matching blocks).
	for _, e := range s.Entities {
		e.X += float64(originX)
		e.Y += float64(originY)
		e.Z += float64(originZ)
		t.AddEntity(e)
	}
	for p, typeName := range s.BlockEntities {
		t.AddBlockEntity(world.Position{X: originX + p.X, Y: originY + p.Y, Z: originZ + p.Z}, typeName)
	}
	if s.Biome != "" {
		t.SetBiome(s.Biome)
	}
	return t
}

// parsePalette splits a Sponge palette entry into its base name and
// property map. "minecraft:oak_stairs[facing=north,half=bottom]" →
// ("minecraft:oak_stairs", {"facing":"north","half":"bottom"}).
// Returns (name, nil) when there are no brackets.
func parsePalette(s string) (string, map[string]string) {
	i := strings.Index(s, "[")
	if i < 0 {
		return s, nil
	}
	name := s[:i]
	j := strings.LastIndex(s, "]")
	if j <= i {
		return name, nil
	}
	body := s[i+1 : j]
	if body == "" {
		return name, nil
	}
	props := make(map[string]string)
	for _, pair := range strings.Split(body, ",") {
		eq := strings.Index(pair, "=")
		if eq <= 0 {
			continue
		}
		props[strings.TrimSpace(pair[:eq])] = strings.TrimSpace(pair[eq+1:])
	}
	if len(props) == 0 {
		return name, nil
	}
	return name, props
}

// stripProperties drops the "[key=val,...]" suffix from a block name.
// "minecraft:oak_stairs[facing=north,half=bottom]" → "minecraft:oak_stairs"
//
// Kept for tests and any caller that wants the base name without
// allocating a property map.
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
