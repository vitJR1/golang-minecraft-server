package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// smallestPNG is a 1×1 transparent PNG (valid bytes). We don't validate
// dimensions, so this works for the "loads and encodes" path.
var smallestPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

func TestLoadFaviconEncodes(t *testing.T) {
	t.Cleanup(func() { favicon.Store(nil) })

	path := filepath.Join(t.TempDir(), "server-icon.png")
	if err := os.WriteFile(path, smallestPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	LoadFavicon(path)

	got := currentFavicon()
	if got == "" {
		t.Fatal("favicon not loaded")
	}
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Errorf("favicon missing data URL prefix: %q", got[:30])
	}
	// Decode and compare bytes.
	encoded := strings.TrimPrefix(got, "data:image/png;base64,")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(smallestPNG) {
		t.Errorf("decoded favicon differs from source PNG")
	}
}

func TestLoadFaviconMissing(t *testing.T) {
	t.Cleanup(func() { favicon.Store(nil) })

	LoadFavicon(filepath.Join(t.TempDir(), "does-not-exist.png"))
	if got := currentFavicon(); got != "" {
		t.Errorf("missing file should leave favicon empty, got %q", got[:30])
	}
}

func TestLoadFaviconEmptyFile(t *testing.T) {
	t.Cleanup(func() { favicon.Store(nil) })

	path := filepath.Join(t.TempDir(), "empty.png")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	LoadFavicon(path)
	if got := currentFavicon(); got != "" {
		t.Errorf("empty file should leave favicon empty, got %q", got[:30])
	}
}
