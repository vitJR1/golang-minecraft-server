package title

import (
	"encoding/binary"
	"testing"
)

func TestTimesPayloadDefaults(t *testing.T) {
	p := TimesPayload(0, 40, -1)
	if len(p) != 12 {
		t.Fatalf("length %d", len(p))
	}
	got := [3]uint32{binary.BigEndian.Uint32(p[0:4]), binary.BigEndian.Uint32(p[4:8]), binary.BigEndian.Uint32(p[8:12])}
	if got != [3]uint32{FadeIn, 40, FadeOut} {
		t.Fatalf("times %v", got)
	}
}
