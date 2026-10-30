package server

import (
	"minecraft-server/server/chat"
	"time"
)

// ChatModerator is the interception point for a custom chat bot. The
// server invokes Check on every Sb Chat Message that survived the mute
// gate, BEFORE the per-instance OnChat hook and the broadcast.
//
// Wire it up in main.go after constructing the server:
//
//	srv.ChatModerator = &myBot{...}
//
// Implementations are called from the offending player's readLoop
// goroutine — keep Check fast. Spawn a goroutine if you need to call
// out to a slow classifier; in that case return chat.Verdict{Allow: true}
// synchronously and apply punishment asynchronously via srv.Mutes / etc.
//
// The bot has full access to the Server through the methods it needs:
// look at srv.Mutes, srv.Ops, srv.FindPlayer, etc. For convenience the
// player's *ClientConnection is passed directly so the bot can warn /
// kick a single player without a name lookup.
type ChatModerator interface {
	// Check is consulted once per incoming chat line. Return Allow=true
	// to let the broadcast proceed unmodified, or set Rewrite to publish
	// a cleaned-up version of the text, or Allow=false to drop the line
	// entirely (the sender sees nothing back — emit a system message
	// yourself if you want feedback).
	Check(c *ClientConnection, message string) chat.Verdict
}

// applyChatModerator is the glue called from handler_play's chat path. If
// no moderator is registered it short-circuits to allow. Returns the
// (possibly rewritten) message + allow flag for the broadcast site.
//
// Side effect: applies MuteFor via srv.Mutes if requested by the bot.
func (s *Server) applyChatModerator(c *ClientConnection, message string) (string, bool) {
	if s == nil || s.ChatModerator == nil {
		return message, true
	}
	verdict := s.ChatModerator.Check(c, message)
	if verdict.MuteFor > 0 {
		s.Mutes.Mute(c.playerName, time.Now().Add(verdict.MuteFor))
	}
	return verdict.Apply(message)
}
