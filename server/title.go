package server

import (
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/hud/title"
)

// title.go: on-screen titles ("You died!", trap alerts) — the packet
// rules are server/hud/title; SendTitle sends times → subtitle → title.

// SendTitle shows text (big) and subtitle (small; "" for none) to this
// client with the given fade-in / stay / fade-out ticks (≤0 → vanilla
// defaults).
func (c *ClientConnection) SendTitle(text, subtitle string, fadeIn, stay, fadeOut int) error {
	if err := c.safeWrite(packetid.CbPlaySetTitleAnimationTimes, title.TimesPayload(fadeIn, stay, fadeOut)); err != nil {
		return err
	}
	if err := c.safeWrite(packetid.CbPlaySetSubtitleText, protocol.TextComponent(subtitle)); err != nil {
		return err
	}
	return c.safeWrite(packetid.CbPlaySetTitleText, protocol.TextComponent(text))
}

// sendClearTitles wipes whatever title / subtitle the client is showing and
// (reset = true) restores the default animation times. The client keeps a
// title across server switches, so the join sequence sends this once: a
// "YOU DIED" from another server would otherwise greet the player here.
func (c *ClientConnection) sendClearTitles() error {
	return c.safeWrite(packetid.CbPlayClearTitles, title.ClearPayload)
}

// broadcastTitle shows the title to everyone in the instance.
func (i *Instance) broadcastTitle(text, subtitle string, fadeIn, stay, fadeOut int) {
	i.Players.Broadcast(packetid.CbPlaySetTitleAnimationTimes, title.TimesPayload(fadeIn, stay, fadeOut), -1)
	i.Players.Broadcast(packetid.CbPlaySetSubtitleText, protocol.TextComponent(subtitle), -1)
	i.Players.Broadcast(packetid.CbPlaySetTitleText, protocol.TextComponent(text), -1)
}
