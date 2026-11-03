// Package title is the on-screen title packets. Vanilla 1.20.1 shows a
// title when Set Title Text arrives; the subtitle and the animation times
// must already be set, so a title is sent as times → subtitle → title, the
// texts plain chat components (protocol.TextComponent).
//
// Packet ids: 0x5D Set Subtitle Text, 0x5F Set Title Text, 0x60 Set Title
// Animation Times, 0x0E Clear Titles.
package title

import "minecraft-server/protocol"

// Default title timing in ticks (vanilla: 10 / 70 / 20).
const (
	FadeIn  = 10
	Stay    = 70
	FadeOut = 20
)

// ClearPayload is Clear Titles with reset = true: the title and subtitle go
// and the animation times return to the defaults.
var ClearPayload = []byte{1}

// TimesPayload encodes Set Title Animation Times: three Int32 tick
// counts, vanilla defaults for non-positive values.
func TimesPayload(fadeIn, stay, fadeOut int) []byte {
	if fadeIn <= 0 {
		fadeIn = FadeIn
	}
	if stay <= 0 {
		stay = Stay
	}
	if fadeOut <= 0 {
		fadeOut = FadeOut
	}
	out := make([]byte, 0, 12)
	out = append(out, protocol.WriteInt(int32(fadeIn))...)
	out = append(out, protocol.WriteInt(int32(stay))...)
	out = append(out, protocol.WriteInt(int32(fadeOut))...)
	return out
}
