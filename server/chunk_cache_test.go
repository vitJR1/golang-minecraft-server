package server

import (
	"bytes"
	"math/rand"
	"testing"

	"minecraft-server/world"
)

// payloads is what a bake streams: each column's Chunk Data payload, in order.
func payloads(t *testing.T, cc *chunkCache, w world.World) [][]byte {
	t.Helper()
	var out [][]byte
	for _, f := range cc.frames(w) {
		out = append(out, f.Build())
	}
	return out
}

// TestChunkCacheMatchesFreshBake: whatever changes between joins — blocks
// set and cleared, a column emptied, the map growing and shrinking, block
// entities, the biome — the cached stream equals a bake from scratch.
func TestChunkCacheMatchesFreshBake(t *testing.T) {
	w := world.NewMemoryWorld()
	for x := -20; x < 20; x++ {
		for z := -20; z < 20; z++ {
			w.SetBlock(world.Position{X: x, Y: 64, Z: z}, world.Stone)
		}
	}
	var cached chunkCache
	check := func(step string) {
		t.Helper()
		got := payloads(t, &cached, w)
		var fresh chunkCache
		want := payloads(t, &fresh, w)
		if len(got) != len(want) {
			t.Fatalf("%s: %d columns, fresh bake %d", step, len(got), len(want))
		}
		for i := range got {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("%s: column %d differs from a fresh bake", step, i)
			}
		}
	}
	check("first bake")
	check("unchanged")

	rng := rand.New(rand.NewSource(1))
	for i := range 30 {
		p := world.Position{X: rng.Intn(40) - 20, Y: 60 + rng.Intn(10), Z: rng.Intn(40) - 20}
		w.SetBlock(p, []world.Block{world.Stone, world.Obsidian, world.Air}[i%3])
	}
	check("scattered edits")

	for x := 0; x < 16; x++ { // empty column (0,0) completely
		for z := 0; z < 16; z++ {
			for y := 60; y < 70; y++ {
				w.SetBlock(world.Position{X: x, Y: y, Z: z}, world.Air)
			}
		}
	}
	check("column emptied")

	far := world.Position{X: 200, Y: 64, Z: -150}
	w.SetBlock(far, world.Stone)
	check("map grew")
	w.SetBlock(far, world.Air)
	check("map shrank")

	w.AddBlockEntity(world.Position{X: 5, Y: 64, Z: 5}, "minecraft:chest")
	check("block entity")
	w.SetBiome("minecraft:desert")
	check("biome")
}

// TestChunkCacheSharesFrames: a second join reuses the first one's frames
// (no re-bake, no re-compression) while nothing changes, and a change
// rebuilds only its column.
func TestChunkCacheSharesFrames(t *testing.T) {
	w := world.NewMemoryWorld()
	w.SetBlock(world.Position{X: 1, Y: 64, Z: 1}, world.Stone)
	w.SetBlock(world.Position{X: 40, Y: 64, Z: 1}, world.Stone)
	var cc chunkCache
	first := cc.frames(w)
	second := cc.frames(w)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("column %d re-baked with no change", i)
		}
	}
	w.SetBlock(world.Position{X: 1, Y: 65, Z: 1}, world.Stone) // column (0,0)
	third := cc.frames(w)
	rebuilt := 0
	for i := range first {
		if first[i] != third[i] {
			rebuilt++
		}
	}
	if rebuilt != 1 {
		t.Errorf("%d columns rebuilt for one block, want 1", rebuilt)
	}
}
