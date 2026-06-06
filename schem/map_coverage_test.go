package schem

import (
	"strings"
	"testing"

	"minecraft-server/world"
)

// TestBedwarsDotaMapBlocksKnown guards that every block kind used by the
// bundled bedwars map has a constant in world/block.go, so the schematic
// renders fully instead of dropping unknown blocks to air. If this fails after
// a new map drop, run the palette dump and add the missing base names.
func TestBedwarsDotaMapBlocksKnown(t *testing.T) {
	s, err := LoadFile("templates/bedwars/badwars_dota_map.schem")
	if err != nil {
		t.Fatalf("load map: %v", err)
	}

	seen := map[string]bool{}
	var missing []string
	for _, entry := range s.Palette {
		if entry == "" || entry == "minecraft:air" {
			continue
		}
		base := entry
		if i := strings.IndexByte(base, '['); i >= 0 {
			base = base[:i] // drop "[properties]"
		}
		if seen[base] {
			continue
		}
		seen[base] = true
		if _, ok := world.BlockByName(base); !ok {
			missing = append(missing, base)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d block kinds in the map are missing from world/block.go:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}
