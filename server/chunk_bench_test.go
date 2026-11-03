package server

import (
	"testing"

	"minecraft-server/player"
	"minecraft-server/schem"
	"minecraft-server/world"
)

// dotaInstance is an instance over the shipped DOTA map, skipped when the
// schematic isn't in the checkout.
func dotaInstance(tb testing.TB) *Instance {
	tb.Helper()
	tmpl, err := schem.LoadTemplate("../schem/templates/bedwars/badwars_dota_map.schem")
	if err != nil {
		tb.Skipf("DOTA map: %v", err)
	}
	return bareInstance(New(), tmpl.Instantiate())
}

// BenchmarkSendWorldChunksDota is one join's chunk stream on the DOTA map:
// baking, framing and compression (play connections compress).
func BenchmarkSendWorldChunksDota(b *testing.B) {
	inst := dotaInstance(b)
	c := offlineConn(inst, "Joiner", player.Survival, 0, 80, 0)
	c.link.EnableCompression(0x03, nil, 256)
	drain(c)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := c.sendWorldChunks(); err != nil {
			b.Fatal(err)
		}
		drain(c)
	}
}

// BenchmarkSendWorldChunksDotaAfterEdit: a join after one block changed —
// the mid-round case (only that column is re-baked).
func BenchmarkSendWorldChunksDotaAfterEdit(b *testing.B) {
	inst := dotaInstance(b)
	c := offlineConn(inst, "Joiner", player.Survival, 0, 80, 0)
	c.link.EnableCompression(0x03, nil, 256)
	drain(c)
	_ = c.sendWorldChunks() // the first bake
	drain(c)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		inst.World.SetBlock(world.Position{X: 3, Y: 70, Z: 3}, []world.Block{world.Stone, world.Obsidian}[i%2])
		if err := c.sendWorldChunks(); err != nil {
			b.Fatal(err)
		}
		drain(c)
	}
}
