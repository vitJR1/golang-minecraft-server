package server

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makePNG renders a solid w×h PNG and returns its bytes.
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{0x33, 0x99, 0xCC, 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLoadFaviconEncodes(t *testing.T) {
	t.Cleanup(func() { favicon.Store(nil) })

	want := makePNG(t, faviconSize, faviconSize)
	path := filepath.Join(t.TempDir(), "server-icon.png")
	if err := os.WriteFile(path, want, 0o644); err != nil {
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
	if !bytes.Equal(decoded, want) {
		t.Errorf("decoded favicon differs from source PNG")
	}
}

func TestLoadFaviconWrongSize(t *testing.T) {
	t.Cleanup(func() { favicon.Store(nil) })

	path := filepath.Join(t.TempDir(), "server-icon.png")
	if err := os.WriteFile(path, makePNG(t, 1024, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	LoadFavicon(path)
	if got := currentFavicon(); got != "" {
		t.Errorf("oversized icon should be rejected, got %q", got[:30])
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
