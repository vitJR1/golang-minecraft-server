package world

// Property-state resolution for blocks that have variants (stairs facing,
// slab half, log axis, …). Only blocks listed in blockStates participate;
// everything else falls back to its plain-default StateID via BlockByName.
//
// Wire encoding: each block reserves a contiguous range [MinStateID ..
// MinStateID + product(num_values) - 1]. Within the range, the offset
// from MinStateID is computed by treating the property list as a
// multi-dimensional index — properties iterated in declaration order,
// rightmost property being the fastest-varying. That matches the layout
// the vanilla client expects, and matches `defaultState` from
// minecraft-data's blocks.json (which we cross-checked when generating
// this table).
//
// Bools encode as ["true", "false"] — Minecraft's BlockState orders the
// "true" branch first.
//
// To add a block: read its entry from blocks.json, paste
//   MinStateID = block.minStateId
//   DefaultStateID = block.defaultState
//   Properties = block.states (preserving order)

type stateProperty struct {
	Name   string
	Values []string
}

type blockStateInfo struct {
	MinStateID     int32
	DefaultStateID int32
	Properties     []stateProperty
}

var blockStates = map[string]blockStateInfo{
	"minecraft:grass_block": {MinStateID: 8, DefaultStateID: 9, Properties: []stateProperty{
		{Name: "snowy", Values: []string{"true", "false"}},
	}},
	"minecraft:podzol": {MinStateID: 12, DefaultStateID: 13, Properties: []stateProperty{
		{Name: "snowy", Values: []string{"true", "false"}},
	}},
	"minecraft:oak_log": {MinStateID: 130, DefaultStateID: 131, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:spruce_log": {MinStateID: 133, DefaultStateID: 134, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:birch_log": {MinStateID: 136, DefaultStateID: 137, Properties: []stateProperty{
		{Name: "axis", Values: []string{"x", "y", "z"}},
	}},
	"minecraft:oak_stairs": {MinStateID: 2874, DefaultStateID: 2885, Properties: []stateProperty{
		{Name: "facing", Values: []string{"north", "south", "west", "east"}},
		{Name: "half", Values: []string{"top", "bottom"}},
		{Name: "shape", Values: []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
	"minecraft:oak_slab": {MinStateID: 11021, DefaultStateID: 11024, Properties: []stateProperty{
		{Name: "type", Values: []string{"top", "bottom", "double"}},
		{Name: "waterlogged", Values: []string{"true", "false"}},
	}},
}

// ResolveStateID returns the wire-level state ID for a block name plus the
// given property map. The name is expected with the namespace prefix
// ("minecraft:oak_stairs"). Unknown property names are ignored; unknown
// values for a known property fall back to whatever index that property
// has in the block's defaultState. Missing properties also fall back to
// the default.
//
// If the block has no entry in blockStates (i.e. no variants registered),
// returns its default StateID via the plain byName lookup. Returns 0 for
// completely unknown blocks (which the caller should treat as "skip" /
// air).
func ResolveStateID(name string, props map[string]string) int32 {
	info, hasStates := blockStates[name]
	if !hasStates {
		if b, ok := byName[name]; ok {
			return b.StateID
		}
		return 0
	}
	if len(info.Properties) == 0 {
		return info.DefaultStateID
	}

	// Strides right-to-left so the last property is fastest-varying.
	strides := make([]int32, len(info.Properties))
	strides[len(strides)-1] = 1
	for i := len(strides) - 2; i >= 0; i-- {
		strides[i] = strides[i+1] * int32(len(info.Properties[i+1].Values))
	}

	// Precompute per-property index of the block's default state so we can
	// fall back property-by-property when the caller hasn't supplied one.
	diff := info.DefaultStateID - info.MinStateID
	var offset int32
	for i, p := range info.Properties {
		n := int32(len(p.Values))
		defaultIdx := diff / strides[i] % n

		idx := defaultIdx
		if val, ok := props[p.Name]; ok {
			for j, v := range p.Values {
				if v == val {
					idx = int32(j)
					break
				}
			}
		}
		offset += idx * strides[i]
	}
	return info.MinStateID + offset
}
