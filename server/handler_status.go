package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"minecraft-server/cfg"
	"minecraft-server/protocol"
	"strings"
	"unicode/utf8"
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

// centerLine pads text with leading spaces so it sits roughly in the
// middle of a motdWidth-column area. Counts runes (not bytes) so the
// "·" mid-dot lines stay aligned. No-op if text is already wider than
// the target.
func centerLine(text string) string {
	pad := (motdWidth - utf8.RuneCountInString(text)) / 2
	if pad <= 0 {
		return text
	}
	return strings.Repeat(" ", pad) + text
}

// hubPromoLines is the decorative tail shown in the server-list hover
// tooltip. Used as a standalone list when nobody's online, or appended
// after real player names when someone is. § codes are legacy color
// codes — vanilla still honors them inside `name`.
var hubPromoLines = []string{
	"§7§m───────────────────────",
	"§6  Welcome to GoLang Minecraft Server!",
	"§7§m───────────────────────",
}

// buildPlayerSample returns the {name, id} entries that go into
// players.sample. Layout:
//
//   - 0 players online:  pure decorative promo lines
//   - 1+ players online: up to (sampleSlots - len(promo)) real names,
//     then a separator, then the promo lines
//
// UUIDs are derived offline via protocol.OfflineUUID — the client only
// uses them to dedupe entries, so anything stable per-name works.
func buildPlayerSample(s *Server) []map[string]any {
	names := s.PlayerNames()
	if len(names) == 0 {
		return promoOnlySample()
	}
	maxReal := sampleSlots - len(hubPromoLines)
	if len(names) > maxReal {
		names = names[:maxReal]
	}
	out := make([]map[string]any, 0, len(names)+len(hubPromoLines))
	for _, n := range names {
		out = append(out, map[string]any{
			"name": n,
			"id":   protocol.OfflineUUID(n),
		})
	}
	for _, line := range hubPromoLines {
		out = append(out, map[string]any{
			"name": line,
			"id":   protocol.OfflineUUID(line), // stable per-line UUID
		})
	}
	return out
}

func promoOnlySample() []map[string]any {
	out := make([]map[string]any, 0, len(hubPromoLines))
	for _, line := range hubPromoLines {
		out = append(out, map[string]any{
			"name": line,
			"id":   protocol.OfflineUUID(line),
		})
	}
	return out
}

func (c *ClientConnection) handleStatus(packet *bytes.Buffer, packetID int) error {
	switch packetID {
	case SbStatusRequest:
		slog.Debug("status request", "addr", c.conn.RemoteAddr().String())
		resp := map[string]any{
			"version": map[string]any{
				"name":     "1.20.1",
				"protocol": 763,
			},
			"players": map[string]any{
				"max":    cfg.MaxPlayers,
				"online": c.server.PlayerCount(),
				"sample": buildPlayerSample(c.server),
			},
			"description": map[string]any{
				"text": "",
				"extra": []map[string]any{
					{"text": centerLine("GoLang Mini-Games") + "\n", "color": "gold", "bold": true},
					{"text": centerLine("FFA · BedWars · SkyWars"), "color": "aqua"},
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
		// Favicon is optional — included only if LoadFavicon found a PNG
		// at startup. The "data:image/png;base64,…" prefix is part of
		// the wire format (vanilla expects exactly that scheme).
		if icon := currentFavicon(); icon != "" {
			resp["favicon"] = icon
		}
		data, _ := json.Marshal(resp)
		return c.safeWrite(CbStatusResponse, protocol.WriteString(string(data)))

	case SbStatusPing:
		slog.Debug("status ping", "addr", c.conn.RemoteAddr().String())
		payload := make([]byte, 8)
		if _, err := packet.Read(payload); err != nil {
			return fmt.Errorf("reading ping payload: %w", err)
		}
		if err := c.safeWrite(CbStatusPong, payload); err != nil {
			return err
		}
		// Status handshakes are one-shot — close after pong.
		c.cleanup()
		return nil

	default:
		return fmt.Errorf("unknown status packet: 0x%02X", packetID)
	}
}
