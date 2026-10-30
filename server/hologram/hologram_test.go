package hologram

import (
	"strings"
	"testing"

	"minecraft-server/protocol"
)

func TestHologramPayloads(t *testing.T) {
	h := &Entity{EID: 7, X: 1, Y: 2, Z: 3, Label: "§bDiamond"}
	spawn := h.SpawnPayload()
	if len(spawn) != 1+16+1+24+3+1+6 {
		t.Errorf("spawn payload length %d", len(spawn))
	}
	if spawn[17] != byte(armorStandEntityTypeID) {
		t.Errorf("entity type = %d, want armor stand (%d)", spawn[17], armorStandEntityTypeID)
	}
	meta := h.MetadataPayload()
	if meta[0] != 7 || meta[len(meta)-1] != 0xFF {
		t.Errorf("metadata framing: % x", meta)
	}
	// Invisible flag, then the custom name as a JSON chat component.
	if meta[1] != protocol.MetaEntityFlags || meta[2] != protocol.MetaTypeByte || meta[3] != protocol.EntityFlagInvisible {
		t.Errorf("entity flags entry: % x", meta[1:4])
	}
	if !strings.Contains(string(meta), `{"text":"§bDiamond"}`) {
		t.Errorf("custom name JSON missing: %q", meta)
	}
	if !strings.HasSuffix(string(meta[:len(meta)-1]), string([]byte{metaArmorStandFlags, protocol.MetaTypeByte, flagStandSmall | flagStandMarker})) {
		t.Errorf("armor stand flags entry missing: % x", meta)
	}
}
