package schem

import (
	"testing"

	"minecraft-server/nbt"
	"minecraft-server/world"
)

func frameNBT(id string, x, y, z float64, facing, rot int8, item string) nbt.Compound {
	c := nbt.Compound{
		"Id": nbt.String(id),
		"Pos": nbt.List{ElemTag: nbt.TagDouble, Items: []nbt.Value{
			nbt.Double(x), nbt.Double(y), nbt.Double(z),
		}},
		"Facing":       nbt.Byte(facing),
		"ItemRotation": nbt.Byte(rot),
	}
	if item != "" {
		c["Item"] = nbt.Compound{"id": nbt.String(item), "Count": nbt.Byte(1)}
	}
	return c
}

func TestParseEntitiesItemFrames(t *testing.T) {
	list := nbt.List{ElemTag: nbt.TagCompound, Items: []nbt.Value{
		frameNBT("minecraft:item_frame", 1.5, 64, 2.5, 3, 4, "minecraft:diamond"),
		frameNBT("minecraft:glow_item_frame", 5, 70, 6, 5, 0, ""),
		frameNBT("minecraft:zombie", 0, 0, 0, 0, 0, ""), // not a frame → skipped
	}}
	ents := parseEntities(list)
	if len(ents) != 2 {
		t.Fatalf("got %d entities, want 2 (zombie skipped)", len(ents))
	}

	a := ents[0]
	if a.Type != "minecraft:item_frame" || a.Frame == nil {
		t.Fatalf("first entity wrong: %+v", a)
	}
	if a.X != 1.5 || a.Y != 64 || a.Z != 2.5 {
		t.Errorf("pos: got (%v,%v,%v)", a.X, a.Y, a.Z)
	}
	if a.Frame.Facing != 3 || a.Frame.Rotation != 4 || a.Frame.Item != "minecraft:diamond" || a.Frame.Glowing {
		t.Errorf("frame data: %+v", a.Frame)
	}

	b := ents[1]
	if b.Type != "minecraft:glow_item_frame" || !b.Frame.Glowing || b.Frame.Item != "" {
		t.Errorf("glow frame wrong: %+v / %+v", b, b.Frame)
	}
}

func TestToTemplateAtShiftsEntities(t *testing.T) {
	s := &Schematic{Width: 1, Height: 1, Length: 1, Palette: []string{"minecraft:air"}, Blocks: []int32{0}}
	s.Entities = parseEntities(nbt.List{ElemTag: nbt.TagCompound, Items: []nbt.Value{
		frameNBT("minecraft:item_frame", 1, 2, 3, 2, 0, ""),
	}})
	tmpl := s.ToTemplateAt(100, 64, -50)
	got := tmpl.Entities()
	if len(got) != 1 {
		t.Fatalf("got %d entities, want 1", len(got))
	}
	if got[0].X != 101 || got[0].Y != 66 || got[0].Z != -47 {
		t.Errorf("shifted pos: got (%v,%v,%v), want (101,66,-47)", got[0].X, got[0].Y, got[0].Z)
	}
}

func TestParseBlockEntities(t *testing.T) {
	list := nbt.List{ElemTag: nbt.TagCompound, Items: []nbt.Value{
		nbt.Compound{"Id": nbt.String("minecraft:bed"), "Pos": nbt.IntArray{1, 64, 2}},
		nbt.Compound{"Id": nbt.String("minecraft:chest"), "Pos": nbt.IntArray{3, 65, 4}},
		nbt.Compound{"Pos": nbt.IntArray{0, 0, 0}}, // no Id → skipped
	}}
	be := parseBlockEntities(list)
	if len(be) != 2 {
		t.Fatalf("got %d block entities, want 2", len(be))
	}
	if be[world.Position{X: 1, Y: 64, Z: 2}] != "minecraft:bed" {
		t.Errorf("bed not parsed: %v", be)
	}
}

func TestRealMapHasBedBlockEntities(t *testing.T) {
	s, err := LoadFile("templates/bedwars/badwars_dota_map.schem")
	if err != nil {
		t.Fatal(err)
	}
	beds := 0
	for _, typ := range s.BlockEntities {
		if typ == "minecraft:bed" {
			beds++
		}
	}
	if beds == 0 {
		t.Errorf("expected bed block entities in the map, got block-entity map of size %d", len(s.BlockEntities))
	}
}
