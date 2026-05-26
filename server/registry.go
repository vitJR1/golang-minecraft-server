package server

import (
	_ "embed"
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
