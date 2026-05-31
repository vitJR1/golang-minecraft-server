package server

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
)

// DefaultFaviconPath is where LoadFavicon looks for the server-list icon.
// Convention matches the vanilla server: server-icon.png in the working
// directory, 64×64 PNG. Any other size is sent as-is and might render
// blurry / squished depending on the client.
const DefaultFaviconPath = "server-icon.png"

// favicon caches the encoded "data:image/png;base64,…" string. Loaded
// once at startup via LoadFavicon; status handler reads atomically so
// a future /reload command could replace it at runtime without locks.
var favicon atomic.Pointer[string]

// LoadFavicon reads a PNG from path, base64-encodes it as a data URL,
// and caches the result. Missing file → no favicon (status omits the
// field). Anything else (bad permissions, read error) is logged but
// not fatal — the server still boots without an icon.
//
// PNG bytes aren't validated beyond "non-empty". Vanilla servers serve
// whatever you put in server-icon.png; same here. The 1.20.1 client
// expects exactly 64×64; smaller/larger usually still renders but
// scaled.
func LoadFavicon(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Info("favicon: not present, status will be icon-less", "path", path)
		} else {
			slog.Warn("favicon: load failed", "path", path, "err", err)
		}
		favicon.Store(nil)
		return
	}
	if len(data) == 0 {
		slog.Warn("favicon: file is empty", "path", path)
		favicon.Store(nil)
		return
	}
	encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	favicon.Store(&encoded)
	slog.Info("favicon: loaded", "path", path, "bytes", len(data))
}

// currentFavicon returns the cached data URL or "" if no favicon set.
func currentFavicon() string {
	if p := favicon.Load(); p != nil {
		return *p
	}
	return ""
}
