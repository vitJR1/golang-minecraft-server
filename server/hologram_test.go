package server

import (
	"strings"
	"testing"

	"minecraft-server/world"
)

func TestHologramSpawnUpdateRemove(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	h := inst.SpawnHologram(0.5, 72, 0.5, "hello")
	if h == nil {
		t.Fatal("SpawnHologram returned nil")
	}
	if len(inst.holograms) != 1 || h.Text() != "hello" {
		t.Fatalf("hologram not registered: %d, text %q", len(inst.holograms), h.Text())
	}
	h.SetText("world")
	if h.Text() != "world" {
		t.Errorf("SetText: %q", h.Text())
	}
	h.Remove()
	h.Remove() // idempotent
	if len(inst.holograms) != 0 {
		t.Errorf("hologram still registered after Remove: %d", len(inst.holograms))
	}
}

func TestHologramPayloads(t *testing.T) {
	h := &hologram{eid: 7, x: 1, y: 2, z: 3, text: "§bDiamond"}
	h.uuid = entityUUID(h.eid)
	spawn := spawnHologramPayload(h)
	if len(spawn) != 1+16+1+24+3+1+6 {
		t.Errorf("spawn payload length %d", len(spawn))
	}
	if spawn[17] != byte(armorStandEntityTypeID) {
		t.Errorf("entity type = %d, want armor stand (%d)", spawn[17], armorStandEntityTypeID)
	}
	meta := hologramMetadataPayload(h)
	if meta[0] != 7 || meta[len(meta)-1] != 0xFF {
		t.Errorf("metadata framing: % x", meta)
	}
	// Invisible flag, then the custom name as a JSON chat component.
	if meta[1] != metaEntityFlags || meta[2] != metaTypeByte || meta[3] != flagInvisible {
		t.Errorf("entity flags entry: % x", meta[1:4])
	}
	if !strings.Contains(string(meta), `{"text":"§bDiamond"}`) {
		t.Errorf("custom name JSON missing: %q", meta)
	}
	if !strings.HasSuffix(string(meta[:len(meta)-1]), string([]byte{metaArmorStandFlags, metaTypeByte, flagStandSmall | flagStandMarker})) {
		t.Errorf("armor stand flags entry missing: % x", meta)
	}
}
