// Package status builds the server-list (Status state) response: the
// version, the player count and hover sample, the centred MOTD and the
// favicon, as the JSON the client expects.
package status

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"minecraft-server/protocol"
)

// sampleSlots is the maximum entries vanilla clients render in the
// server-list hover tooltip. Anything past this is silently dropped.
const sampleSlots = 12

// motdWidth is the approximate column count we centre MOTD lines to.
// Minecraft's MOTD uses a proportional font so this is a hand-tuned
// average — narrow enough that long lines stay padded, wide enough
// that short lines aren't crammed at the edge. Bump if your lines
// look left-shifted on a wide window.
const motdWidth = 45

// CenterLine pads text with leading spaces so it sits roughly in the
// middle of a motdWidth-column area. Counts runes (not bytes) so the
// "·" mid-dot lines stay aligned. No-op if text is already wider than
// the target.
func CenterLine(text string) string {
	pad := (motdWidth - utf8.RuneCountInString(text)) / 2
	if pad <= 0 {
		return text
	}
	return strings.Repeat(" ", pad) + text
}

// promoLines is the decorative tail shown in the server-list hover
// tooltip. Used as a standalone list when nobody's online, or appended
// after real player names when someone is. § codes are legacy color
// codes — vanilla still honors them inside `name`.
var promoLines = []string{
	"§7§m───────────────────────",
	"§6  Welcome to GoLang Minecraft Server!",
	"§7§m───────────────────────",
}

// Sample returns the {name, id} entries that go into players.sample.
// Layout:
//
//   - 0 players online:  pure decorative promo lines
//   - 1+ players online: up to (sampleSlots - len(promo)) real names,
//     then the promo lines
//
// UUIDs are derived offline via protocol.OfflineUUID — the client only
// uses them to dedupe entries, so anything stable per-name works.
func Sample(names []string) []map[string]any {
	maxReal := sampleSlots - len(promoLines)
	if len(names) > maxReal {
		names = names[:maxReal]
	}
	out := make([]map[string]any, 0, len(names)+len(promoLines))
	for _, n := range names {
		out = append(out, map[string]any{"name": n, "id": protocol.OfflineUUID(n)})
	}
	for _, line := range promoLines {
		// stable per-line UUID
		out = append(out, map[string]any{"name": line, "id": protocol.OfflineUUID(line)})
	}
	return out
}

// Info is what the response reports.
type Info struct {
	Online, Max int
	Names       []string // online players, for the hover sample
	Favicon     string   // "data:image/png;base64,…" or ""
}

// Response is the Status Response JSON for info.
func Response(info Info) []byte {
	resp := map[string]any{
		"version": map[string]any{
			"name":     "1.20.1",
			"protocol": 763,
		},
		"players": map[string]any{
			"max":    info.Max,
			"online": info.Online,
			"sample": Sample(info.Names),
		},
		"description": map[string]any{
			"text": "",
			"extra": []map[string]any{
				{"text": CenterLine("GoLang Mini-Games") + "\n", "color": "gold", "bold": true},
				{"text": CenterLine("FFA · BedWars · SkyWars"), "color": "aqua"},
			},
		},
		// We don't implement signed chat (added in 1.19.1). Declaring
		// false explicitly drops the yellow exclamation icon next to
		// the server name in the list. The big "this server may
		// resend chat messages" warning at first connect still
		// shows — that's gated on the actual chat-session handshake,
		// not this flag.
		"enforcesSecureChat": false,
	}
	// Favicon is optional. The "data:image/png;base64,…" prefix is part of
	// the wire format (vanilla expects exactly that scheme).
	if info.Favicon != "" {
		resp["favicon"] = info.Favicon
	}
	data, _ := json.Marshal(resp)
	return data
}
