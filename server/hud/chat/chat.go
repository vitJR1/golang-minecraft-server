// Package chat is the chat line format, the System Chat payload, and what
// a chat moderator (a bot, e.g. anti-spam / anti-flame) can decide about a
// message. The server runs the pipeline — validation, the auth gate, mutes,
// the moderator, the game's OnChat hook, the broadcast (handler_play.go).
package chat

import (
	"encoding/json"
	"fmt"
	"time"

	"minecraft-server/protocol"
)

// Line formats a chat line: "<sender> message", or the bare message for an
// empty sender (a server line).
func Line(sender, message string) string {
	if sender == "" {
		return message
	}
	return fmt.Sprintf("<%s> %s", sender, message)
}

// SystemPayload assembles a System Chat (Cb 0x64) body: JSON chat component
// string + overlay bool. encoding/json handles escaping so arbitrary text
// can't break the wire format.
func SystemPayload(text string) []byte {
	encoded, _ := json.Marshal(map[string]string{"text": text})
	return append(protocol.WriteString(string(encoded)), 0)
}

// Verdict is a moderator's decision about one message.
//
// Examples:
//
//	allow as-is:        Verdict{Allow: true}
//	rewrite (censor):   Verdict{Allow: true, Rewrite: "****"}
//	drop silently:      Verdict{Allow: false}
//	drop + auto-mute:   Verdict{Allow: false, MuteFor: 5*time.Minute}
//
// MuteFor, if non-zero, makes the server mute the sender for that long — a
// convenience for the common "second strike = mute" pattern.
type Verdict struct {
	Allow   bool
	Rewrite string
	MuteFor time.Duration
}

// Apply is the message to broadcast after the verdict, and whether to
// broadcast at all.
func (v Verdict) Apply(message string) (string, bool) {
	if !v.Allow {
		return "", false
	}
	if v.Rewrite != "" {
		return v.Rewrite, true
	}
	return message, true
}
