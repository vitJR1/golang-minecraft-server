// Package world models the game world: blocks at integer positions, plus a
// minimal World interface so swappable storage backends (memory, region
// files) can live behind the same API.
package world

// Block is a single block state. The StateID is the global state index used
// in chunk-data palettes on the wire (vanilla 1.20.1 has ~26000 of them);
// Name is the namespaced identifier ("minecraft:stone").
//
// Today we hand-roll a few well-known constants. A future codegen step can
// dump the full registry from blocks.json.
type Block struct {
	StateID int32
	Name    string
}

// Vanilla 1.20.1 block-state IDs. Source: server -reports JSON dump
// ("minecraft:block_states"). Pick a small palette for now.
var (
	Air         = Block{StateID: 0, Name: "minecraft:air"}
	Stone       = Block{StateID: 1, Name: "minecraft:stone"}
	GrassBlock  = Block{StateID: 9, Name: "minecraft:grass_block"}
	Dirt        = Block{StateID: 10, Name: "minecraft:dirt"}
	Cobblestone = Block{StateID: 14, Name: "minecraft:cobblestone"}
	Bedrock     = Block{StateID: 79, Name: "minecraft:bedrock"}
)
