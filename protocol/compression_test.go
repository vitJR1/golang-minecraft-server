package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompressionRoundTrip(t *testing.T) {
	cases := [][]byte{
		[]byte("hello"),
		bytes.Repeat([]byte{0xAB}, 1024),
		[]byte(strings.Repeat("Minecraft ", 500)),
	}
	for i, src := range cases {
		zipped, err := CompressPayload(src)
		if err != nil {
			t.Fatalf("case %d compress: %v", i, err)
		}
		got, err := DecompressPayload(zipped, len(src))
		if err != nil {
			t.Fatalf("case %d decompress: %v", i, err)
		}
		if !bytes.Equal(got, src) {
			t.Errorf("case %d: round-trip mismatch", i)
		}
	}
}

func TestCompressionActuallyShrinksRepeats(t *testing.T) {
	src := bytes.Repeat([]byte("ABCDEFGH"), 1000) // 8000 bytes, very repetitive
	zipped, err := CompressPayload(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(zipped) >= len(src) {
		t.Errorf("compression did not shrink: %d -> %d", len(src), len(zipped))
	}
}

func TestDecompressWrongSize(t *testing.T) {
	src := []byte("hello world")
	zipped, _ := CompressPayload(src)

	if _, err := DecompressPayload(zipped, len(src)+5); err == nil {
		t.Error("expected error when declared size exceeds actual")
	}
	if _, err := DecompressPayload(zipped, len(src)-1); err == nil {
		t.Error("expected error when declared size is below actual")
	}
}

func TestDecompressNegativeSize(t *testing.T) {
	if _, err := DecompressPayload([]byte{0x78, 0x9c}, -1); err == nil {
		t.Error("expected error for negative size")
	}
}

func TestDecompressGarbage(t *testing.T) {
	if _, err := DecompressPayload([]byte{0x00, 0x00, 0x00, 0x00}, 10); err == nil {
		t.Error("expected error for non-zlib bytes")
	}
}
