package server

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"minecraft-server/nbt"
	"sync"
)

//go:embed registry-codec.json
var registryJSON []byte

// registryHints lists the NBT type per ambiguous key in the 1.20.1 registry
// codec. JSON's single number type can't distinguish Byte/Int/Long/Float/
// Double — vanilla picks specific types per field, and the client rejects
// the codec if any of them are wrong.
var registryHints = nbt.TypeHints{
	ByteKeys: map[string]bool{
		"bed_works":             true,
		"has_ceiling":           true,
		"has_precipitation":     true,
		"has_raids":             true,
		"has_skylight":          true,
		"italic":                true,
		"natural":               true,
		"piglin_safe":           true,
		"replace_current_music": true,
		"respawn_anchor_works":  true,
		"ultrawarm":             true,
	},
	FloatKeys: map[string]bool{
		"ambient_light":    true,
		"downfall":         true,
		"exhaustion":       true,
		"item_model_index": true,
		"probability":      true,
		"temperature":      true,
		"tick_chance":      true,
	},
	DoubleKeys: map[string]bool{
		"coordinate_scale": true,
	},
	LongKeys: map[string]bool{
		"fixed_time": true,
	},
}

var (
	registryOnce  sync.Once
	registryBytes []byte
)

// RegistryCodec returns the NBT bytes of the registry codec sent inside the
// Login (Play) packet. Built once and cached.
func RegistryCodec() []byte {
	registryOnce.Do(func() {
		root, err := nbt.FromJSONBytes(registryJSON, registryHints)
		if err != nil {
			panic(fmt.Errorf("parse registry codec: %w", err))
		}
		registryBytes = nbt.Marshal(root)
	})
	return registryBytes
}

// DefaultBiome is the fallback biome name when a world doesn't specify one (or
// names an unknown biome). Plains is a sensible neutral overworld biome.
const DefaultBiome = "minecraft:plains"

var (
	biomeOnce sync.Once
	biomeIDs  map[string]int32
)

// BiomeID returns the registry index of a namespaced biome name (the value the
// chunk-data biome container expects), and whether it's known. Parsed once
// from the embedded registry codec's minecraft:worldgen/biome registry.
func BiomeID(name string) (int32, bool) {
	biomeOnce.Do(func() {
		biomeIDs = map[string]int32{}
		var codec struct {
			Biome struct {
				Value []struct {
					Name string `json:"name"`
					ID   int32  `json:"id"`
				} `json:"value"`
			} `json:"minecraft:worldgen/biome"`
		}
		if err := json.Unmarshal(registryJSON, &codec); err == nil {
			for _, b := range codec.Biome.Value {
				biomeIDs[b.Name] = b.ID
			}
		}
	})
	id, ok := biomeIDs[name]
	return id, ok
}

// biomeIDOrDefault resolves name to its registry index, falling back to the
// default biome (and then 0) when name is empty or unknown.
func biomeIDOrDefault(name string) int32 {
	if name == "" {
		name = DefaultBiome
	}
	if id, ok := BiomeID(name); ok {
		return id
	}
	if id, ok := BiomeID(DefaultBiome); ok {
		return id
	}
	return 0
}
