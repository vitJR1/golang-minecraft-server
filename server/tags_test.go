package server

import (
	"bytes"
	"testing"

	"minecraft-server/protocol"
)

func TestUpdateTagsPayload(t *testing.T) {
	buf := bytes.NewBuffer(updateTagsPayload(serverTags))
	n, _ := protocol.ReadVarInt(buf)
	if n != 2 {
		t.Fatalf("registries: %d, want 2", n)
	}
	reg, _ := protocol.ReadStringFromBuf(buf)
	if reg != "minecraft:fluid" {
		t.Fatalf("first registry: %q", reg)
	}
	tags, _ := protocol.ReadVarInt(buf)
	if tags != 2 {
		t.Fatalf("fluid tags: %d", tags)
	}
	name, _ := protocol.ReadStringFromBuf(buf)
	cnt, _ := protocol.ReadVarInt(buf)
	a, _ := protocol.ReadVarInt(buf)
	b, _ := protocol.ReadVarInt(buf)
	if name != "minecraft:water" || cnt != 2 || a != 2 || b != 1 {
		t.Errorf("water tag: %s %d [%d %d]", name, cnt, a, b)
	}
	name, _ = protocol.ReadStringFromBuf(buf)
	cnt, _ = protocol.ReadVarInt(buf)
	a, _ = protocol.ReadVarInt(buf)
	b, _ = protocol.ReadVarInt(buf)
	if name != "minecraft:lava" || cnt != 2 || a != 4 || b != 3 {
		t.Errorf("lava tag: %s %d [%d %d]", name, cnt, a, b)
	}
	reg, _ = protocol.ReadStringFromBuf(buf)
	tags, _ = protocol.ReadVarInt(buf)
	name, _ = protocol.ReadStringFromBuf(buf)
	cnt, _ = protocol.ReadVarInt(buf)
	if reg != "minecraft:block" || tags != 1 || name != "minecraft:climbable" || cnt != len(climbableBlockIDs) {
		t.Errorf("block registry: %s %d %s %d", reg, tags, name, cnt)
	}
}
