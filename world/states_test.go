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
