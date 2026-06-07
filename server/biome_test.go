package server

import "testing"

func TestBiomeID(t *testing.T) {
	if id, ok := BiomeID("minecraft:plains"); !ok || id != 39 {
		t.Errorf("plains: got %d,%v want 39,true", id, ok)
	}
	if _, ok := BiomeID("minecraft:not_a_biome"); ok {
		t.Error("unknown biome should return ok=false")
	}
}

func TestBiomeIDOrDefault(t *testing.T) {
	plains, _ := BiomeID(DefaultBiome)
	if got := biomeIDOrDefault(""); got != plains {
		t.Errorf("empty → %d, want default %d", got, plains)
	}
	if got := biomeIDOrDefault("minecraft:nope"); got != plains {
		t.Errorf("unknown → %d, want default %d", got, plains)
	}
	if got := biomeIDOrDefault("minecraft:the_void"); got == plains {
		t.Error("known non-default biome should resolve to its own id")
	}
}
