package server

import (
	"bytes"
	"testing"
)

func TestRegistryCodecParses(t *testing.T) {
	got := RegistryCodec()
	if len(got) == 0 {
		t.Fatal("empty registry codec")
	}
	// First byte must be TagCompound (0x0a) — the root tag.
	if got[0] != 0x0a {
		t.Fatalf("expected root TagCompound (0x0a), got 0x%02x", got[0])
	}
	// Registry must include the four required 1.20.1 registries.
	for _, name := range []string{
		"minecraft:chat_type",
		"minecraft:dimension_type",
		"minecraft:worldgen/biome",
		"minecraft:damage_type",
	} {
		if !bytes.Contains(got, []byte(name)) {
			t.Errorf("registry codec missing %q", name)
		}
	}
}

func TestRegistryCodecCached(t *testing.T) {
	a := RegistryCodec()
	b := RegistryCodec()
	if &a[0] != &b[0] {
		t.Fatal("RegistryCodec did not return the cached byte slice")
	}
}
