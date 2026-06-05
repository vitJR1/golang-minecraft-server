package protocol

import (
	"bytes"
	"testing"
	"fmt"
)

func TestWriteUUIDHyphenated(t *testing.T) {
	got, err := WriteUUID("00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	if !bytes.Equal(got, want) {
		t.Errorf("got %x, want %x", got, want)
	}
}

func TestWriteUUIDUnhyphenated(t *testing.T) {
	hyphenated, _ := WriteUUID("12345678-9abc-def0-1234-567890abcdef")
	flat, _ := WriteUUID("123456789abcdef01234567890abcdef")
	if !bytes.Equal(hyphenated, flat) {
		t.Errorf("hyphenated and flat forms produce different bytes:\n  %x\n  %x", hyphenated, flat)
	}
}

func TestWriteUUIDInvalid(t *testing.T) {
	for _, bad := range []string{
		"",
		"not-a-uuid",
		"12345678-9abc-def0-1234-567890abcde",   // 1 char short
		"12345678-9abc-def0-1234-567890abcdefg", // contains non-hex
		"12345678_9abc_def0_1234_567890abcdef",  // wrong separator
		"00000000000000000000000000000000zz",    // 34 chars
	} {
		if _, err := WriteUUID(bad); err == nil {
			t.Errorf("WriteUUID(%q): want error, got nil", bad)
		}
	}
}

func TestFormatUUIDInsertsHyphens(t *testing.T) {
	got, err := FormatUUID("123456789abcdef01234567890abcdef")
	if err != nil {
		t.Fatal(err)
	}
	want := "12345678-9abc-def0-1234-567890abcdef"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatUUIDWrongLength(t *testing.T) {
	if _, err := FormatUUID("too-short"); err == nil {
		t.Fatal("expected error for short UUID")
	}
}

func TestOfflineUUIDIsDeterministic(t *testing.T) {
	a := OfflineUUID("Notch")
	b := OfflineUUID("Notch")
	if a != b {
		t.Errorf("same name produces different UUIDs: %q vs %q", a, b)
	}
}

func TestOfflineUUIDDistinct(t *testing.T) {
	if OfflineUUID("Alice") == OfflineUUID("Bob") {
		t.Error("different names produced same UUID")
	}
}

func TestOfflineUUIDHasV3Markers(t *testing.T) {
	u := OfflineUUID("anyone")
	// Format is xxxxxxxx-xxxx-Mxxx-Nxxx-xxxxxxxxxxxx where M=3 (version)
	// and N is the variant nibble — must be 8, 9, a or b.
	if len(u) != 36 {
		t.Fatalf("UUID length %d, want 36", len(u))
	}
	if u[14] != '3' {
		t.Errorf("version nibble = %c, want '3' (UUIDv3)", u[14])
	}
	variant := u[19]
	if variant != '8' && variant != '9' && variant != 'a' && variant != 'b' {
		t.Errorf("variant nibble = %c, want one of 8/9/a/b", variant)
	}
}

func TestOfflineUUIDRoundTrip(t *testing.T) {
	// OfflineUUID returns hyphenated form — WriteUUID should accept it directly.
	if _, err := WriteUUID(OfflineUUID("Notch")); err != nil {
		t.Errorf("WriteUUID rejects OfflineUUID output: %v", err)
	}
}

func TestRandomUUIDIsVersion4(t *testing.T) {
	uuid := RandomUUID()

	// version nibble (bits 4-7 of byte 6) must be 4
	if uuid[6]>>4 != 4 {
		t.Errorf("version nibble = %x, want 4", uuid[6]>>4)
	}

	// variant bits (top 2 bits of byte 8) must be 0b10
	if uuid[8]>>6 != 2 {
		t.Errorf("variant bits = %02b, want 10", uuid[8]>>6)
	}
}

func TestRandomUUIDIsUnique(t *testing.T) {
	a := RandomUUID()
	b := RandomUUID()
	if a == b {
		t.Error("two sequential RandomUUID calls returned identical values")
	}
}

func TestRandomUUIDAcceptedByWriteUUID(t *testing.T) {
	// Format to hyphenated string and verify WriteUUID accepts it.
	uuid := RandomUUID()
	s := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
	if _, err := WriteUUID(s); err != nil {
		t.Errorf("WriteUUID rejects RandomUUID output: %v", err)
	}
}