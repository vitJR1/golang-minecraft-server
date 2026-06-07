package schem

import (
	"testing"

	"minecraft-server/nbt"
)

func TestParseDominantBiomeV2Single(t *testing.T) {
	inner := nbt.Compound{
		"BiomePalette":    nbt.Compound{"minecraft:plains": nbt.Int(0)},
		"BiomePaletteMax": nbt.Int(1),
		"BiomeData":       nbt.ByteArray{0, 0, 0, 0},
	}
	if got := parseDominantBiome(inner); got != "minecraft:plains" {
		t.Errorf("got %q, want minecraft:plains", got)
	}
}

func TestParseDominantBiomePicksMostCommon(t *testing.T) {
	inner := nbt.Compound{
		"BiomePalette": nbt.Compound{
			"minecraft:plains": nbt.Int(0),
			"minecraft:forest": nbt.Int(1),
		},
		// data: forest(1) appears 3×, plains(0) once → forest wins.
		"BiomeData": nbt.ByteArray{1, 1, 0, 1},
	}
	if got := parseDominantBiome(inner); got != "minecraft:forest" {
		t.Errorf("got %q, want minecraft:forest", got)
	}
}

func TestParseDominantBiomeNone(t *testing.T) {
	if got := parseDominantBiome(nbt.Compound{}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestRealMapBiome(t *testing.T) {
	s, err := LoadFile("templates/bedwars/badwars_dota_map.schem")
	if err != nil {
		t.Fatal(err)
	}
	if s.Biome != "minecraft:plains" {
		t.Errorf("map biome = %q, want minecraft:plains", s.Biome)
	}
	// And it flows into the template.
	if tmpl := s.ToTemplateAt(0, 0, 0); tmpl.Biome() != "minecraft:plains" {
		t.Errorf("template biome = %q, want minecraft:plains", tmpl.Biome())
	}
}
