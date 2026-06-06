package world

import "testing"

func TestResolveStateIDNoProps(t *testing.T) {
	// Block without variants — defaultState comes from BlockByName.
	if got := ResolveStateID("minecraft:stone", nil); got != 1 {
		t.Errorf("stone: got %d, want 1", got)
	}
	if got := ResolveStateID("minecraft:beacon", nil); got != 7918 {
		t.Errorf("beacon: got %d, want 7918", got)
	}
}

func TestResolveStateIDUnknownBlock(t *testing.T) {
	if got := ResolveStateID("minecraft:nonexistent", nil); got != 0 {
		t.Errorf("nonexistent: got %d, want 0", got)
	}
}

func TestResolveStateIDDefaultsWhenPropsEmpty(t *testing.T) {
	// Block WITH variants but no props supplied → returns its defaultState.
	cases := []struct {
		name string
		want int32
	}{
		{"minecraft:oak_stairs", 2885},
		{"minecraft:oak_slab", 11024},
		{"minecraft:oak_log", 131},
		{"minecraft:grass_block", 9},
	}
	for _, c := range cases {
		if got := ResolveStateID(c.name, nil); got != c.want {
			t.Errorf("%s default: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestResolveStateIDOakLogAxis(t *testing.T) {
	// oak_log: minStateId=130, axis=[x,y,z]; so x=130, y=131, z=132.
	cases := []struct {
		axis string
		want int32
	}{
		{"x", 130}, {"y", 131}, {"z", 132},
	}
	for _, c := range cases {
		got := ResolveStateID("minecraft:oak_log", map[string]string{"axis": c.axis})
		if got != c.want {
			t.Errorf("oak_log axis=%s: got %d, want %d", c.axis, got, c.want)
		}
	}
}

func TestResolveStateIDOakSlabType(t *testing.T) {
	// oak_slab properties (in order): type=[top,bottom,double], waterlogged=[true,false]
	// minStateId=11021. stride: waterlogged=1, type=2.
	// Encoding: 11021 + type_idx*2 + waterlogged_idx*1
	cases := []struct {
		ttype string
		water string
		want  int32
	}{
		{"top", "false", 11021 + 0*2 + 1},    // 11022
		{"bottom", "false", 11021 + 1*2 + 1}, // 11024 (default)
		{"double", "false", 11021 + 2*2 + 1}, // 11026
		{"top", "true", 11021 + 0*2 + 0},     // 11021
		{"bottom", "true", 11021 + 1*2 + 0},  // 11023
	}
	for _, c := range cases {
		got := ResolveStateID("minecraft:oak_slab", map[string]string{
			"type": c.ttype, "waterlogged": c.water,
		})
		if got != c.want {
			t.Errorf("oak_slab type=%s waterlogged=%s: got %d, want %d",
				c.ttype, c.water, got, c.want)
		}
	}
}

func TestResolveStateIDOakStairsFacing(t *testing.T) {
	// oak_stairs properties: facing=[north,south,west,east], half=[top,bottom],
	// shape=[straight,...], waterlogged=[true,false]
	// strides: waterlogged=1, shape=2, half=10, facing=20. minStateId=2874.
	// north + bottom + straight + false = 2874 + 0 + 10 + 0 + 1 = 2885 (default)
	// east  + bottom + straight + false = 2874 + 60 + 10 + 0 + 1 = 2945
	// north + top    + straight + false = 2874 + 0  + 0  + 0 + 1 = 2875
	got := ResolveStateID("minecraft:oak_stairs", map[string]string{
		"facing": "east", "half": "bottom", "shape": "straight", "waterlogged": "false",
	})
	if got != 2945 {
		t.Errorf("east stairs: got %d, want 2945", got)
	}
	got = ResolveStateID("minecraft:oak_stairs", map[string]string{
		"facing": "north", "half": "top", "shape": "straight", "waterlogged": "false",
	})
	if got != 2875 {
		t.Errorf("top stairs: got %d, want 2875", got)
	}
}

func TestResolveStateIDPartialPropsFallsBack(t *testing.T) {
	// Only facing supplied — half/shape/waterlogged should take their
	// defaultState values (bottom, straight, false).
	got := ResolveStateID("minecraft:oak_stairs", map[string]string{"facing": "south"})
	// south + bottom + straight + false = 2874 + 20 + 10 + 0 + 1 = 2905
	if got != 2905 {
		t.Errorf("south w/ defaults: got %d, want 2905", got)
	}
}

func TestResolveStateIDUnknownValueFallsBackToDefault(t *testing.T) {
	// Bad axis value should yield default (y → 131).
	got := ResolveStateID("minecraft:oak_log", map[string]string{"axis": "diagonal"})
	if got != 131 {
		t.Errorf("bad axis falls back: got %d, want 131", got)
	}
}

// TestResolveStateIDMapVariants cross-checks ResolveStateID for stairs, slabs,
// fences, walls, panes, logs, and clusters against state IDs independently
// computed from minecraft-data 1.20 blocks.json. Guards the generated
// blockStates tables (property order + value lists + ranges).
func TestResolveStateIDMapVariants(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]string
		want  int32
	}{
		{"minecraft:diorite_stairs", map[string]string{"facing": "west", "half": "bottom", "shape": "straight", "waterlogged": "false"}, 13912},
		{"minecraft:blackstone_slab", map[string]string{"type": "top", "waterlogged": "false"}, 19725},
		{"minecraft:quartz_slab", map[string]string{"type": "double", "waterlogged": "false"}, 11146},
		{"minecraft:dark_oak_fence", map[string]string{"east": "true", "north": "false", "south": "false", "waterlogged": "false", "west": "true"}, 11599},
		{"minecraft:mossy_stone_brick_wall", map[string]string{"east": "low", "north": "low", "south": "none", "up": "true", "waterlogged": "false", "west": "low"}, 15139},
		{"minecraft:light_blue_stained_glass_pane", map[string]string{"east": "true", "north": "true", "south": "true", "waterlogged": "false", "west": "false"}, 9331},
		{"minecraft:spruce_log", map[string]string{"axis": "z"}, 135},
		{"minecraft:amethyst_cluster", map[string]string{"facing": "up", "waterlogged": "false"}, 20901},
	}
	for _, tc := range cases {
		if got := ResolveStateID(tc.name, tc.props); got != tc.want {
			t.Errorf("ResolveStateID(%s, %v) = %d, want %d", tc.name, tc.props, got, tc.want)
		}
	}
}

func TestBlockEntityTypeIDs(t *testing.T) {
	cases := map[string]int32{
		"minecraft:bed": 24, "minecraft:chest": 1, "minecraft:ender_chest": 3,
		"minecraft:banner": 19, "minecraft:beacon": 14, "minecraft:campfire": 32,
		"minecraft:skull": 15,
	}
	for name, want := range cases {
		if got, ok := BlockEntityTypeID(name); !ok || got != want {
			t.Errorf("BlockEntityTypeID(%s) = %d,%v; want %d", name, got, ok, want)
		}
	}
	if _, ok := BlockEntityTypeID("minecraft:not_a_block_entity"); ok {
		t.Error("unknown type should return ok=false")
	}
}

func TestTemplateInstantiateCopiesBlockEntities(t *testing.T) {
	tmpl := NewTemplate()
	tmpl.AddBlockEntity(Position{X: 1, Y: 2, Z: 3}, "minecraft:bed")
	w := tmpl.Instantiate()
	bep, ok := any(w).(BlockEntityProvider)
	if !ok {
		t.Fatal("MemoryWorld should implement BlockEntityProvider")
	}
	if bep.BlockEntities()[Position{X: 1, Y: 2, Z: 3}] != "minecraft:bed" {
		t.Errorf("block entity not copied on instantiate: %v", bep.BlockEntities())
	}
}
