package server

import (
	"encoding/json"

	"minecraft-server/protocol"
)

// title.go: on-screen titles ("You died!", trap alerts). Vanilla 1.20.1
// shows a title when Set Title Text arrives; the subtitle and the animation
// times must already be set, so sendTitle sends times → subtitle → title.
// Texts are plain JSON chat components, like sendSystemMessage.
//
// Packet ids anchor on Sound Effect 0x62 / System Chat 0x64 in packet_ids.go:
// 0x5D Set Subtitle Text, 0x5F Set Title Text, 0x60 Set Title Animation Times.

// Default title timing in ticks (vanilla: 10 / 70 / 20).
const (
	titleFadeIn  = 10
	titleStay    = 70
	titleFadeOut = 20
)

// sendTitle shows title (big) and subtitle (small; "" for none) to this
// client with the given fade-in / stay / fade-out ticks (≤0 → vanilla
// defaults).
func (c *ClientConnection) sendTitle(title, subtitle string, fadeIn, stay, fadeOut int) error {
	if err := c.safeWrite(CbPlaySetTitleAnimationTimes, titleTimesPayload(fadeIn, stay, fadeOut)); err != nil {
		return err
	}
	if err := c.safeWrite(CbPlaySetSubtitleText, textComponentPayload(subtitle)); err != nil {
		return err
	}
	return c.safeWrite(CbPlaySetTitleText, textComponentPayload(title))
}

// broadcastTitle shows the title to everyone in the instance.
func (i *Instance) broadcastTitle(title, subtitle string, fadeIn, stay, fadeOut int) {
	i.Players.Broadcast(CbPlaySetTitleAnimationTimes, titleTimesPayload(fadeIn, stay, fadeOut), -1)
	i.Players.Broadcast(CbPlaySetSubtitleText, textComponentPayload(subtitle), -1)
	i.Players.Broadcast(CbPlaySetTitleText, textComponentPayload(title), -1)
}

// textComponentPayload encodes a plain text chat component ({"text": …}).
func textComponentPayload(text string) []byte {
	encoded, _ := json.Marshal(map[string]string{"text": text})
	return protocol.WriteString(string(encoded))
}

// titleTimesPayload encodes Set Title Animation Times: three Int32 tick
// counts, vanilla defaults for non-positive values.
func titleTimesPayload(fadeIn, stay, fadeOut int) []byte {
	if fadeIn <= 0 {
		fadeIn = titleFadeIn
	}
	if stay <= 0 {
		stay = titleStay
	}
	if fadeOut <= 0 {
		fadeOut = titleFadeOut
	}
	out := make([]byte, 0, 12)
	out = append(out, protocol.WriteInt(int32(fadeIn))...)
	out = append(out, protocol.WriteInt(int32(stay))...)
	out = append(out, protocol.WriteInt(int32(fadeOut))...)
	return out
}
