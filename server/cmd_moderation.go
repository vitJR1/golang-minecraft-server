package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"minecraft-server/server/moderation"
)

// cmd_moderation.go: /ban /unban /kick /mute /unmute.

func init() {
	registerCommand(&Command{
		Name:     "ban",
		NeedsOp:  true,
		Help:     "/ban <player> <duration> [reason] — 1m, 7d, 2h, 1w",
		Run:      cmdBan,
		Complete: playerArg(1),
	})
	registerCommand(&Command{
		Name:    "unban",
		NeedsOp: true,
		Help:    "/unban <player>",
		Run:     cmdUnban,
	})
	registerCommand(&Command{
		Name:     "kick",
		NeedsOp:  true,
		Help:     "/kick <player> [reason]",
		Run:      cmdKick,
		Complete: playerArg(1),
	})
	registerCommand(&Command{
		Name:     "mute",
		NeedsOp:  true,
		Help:     "/mute <player> <duration> — 30s, 5m, 1h, 7d",
		Run:      cmdMute,
		Complete: playerArg(1),
	})
	registerCommand(&Command{
		Name:     "unmute",
		NeedsOp:  true,
		Help:     "/unmute <player>",
		Run:      cmdUnmute,
		Complete: playerArg(1),
	})
}

// cmdBan: /ban <player> <duration> [reason words…]
//
// Writes a ban expiring at now+duration to the server's ban.Store
// (Postgres or banlist.json depending on STORAGE) and, if the player is
// currently online, kicks them with a Play Disconnect carrying the reason.
// Duration uses our compact format: 30s / 5m / 2h / 7d / 1w.
func cmdBan(c *ClientConnection, args []string) {
	if len(args) < 2 {
		_ = c.sendSystemMessage("Usage: /ban <player> <duration> [reason]")
		return
	}
	target := args[0]
	dur, err := moderation.ParseShortDuration(args[1])
	if err != nil {
		_ = c.sendSystemMessage("Bad duration: " + err.Error())
		return
	}
	reason := "banned by " + c.playerName
	if len(args) > 2 {
		reason = strings.Join(args[2:], " ")
	}
	until := time.Now().Add(dur)
	if err := c.server.Bans.Add(context.Background(), target, reason, c.playerName, until); err != nil {
		slog.Error("ban store write failed", "target", target, "err", err)
		_ = c.sendSystemMessage("Ban failed (storage error): " + err.Error())
		return
	}

	_ = c.sendSystemMessage(fmt.Sprintf("Banned %s until %s — %s",
		target, until.Format("2006-01-02 15:04:05"), reason))
	// A ban of a player at the anticheat max status is the admins' half of
	// the ladder: log it with the admin's reason.
	c.server.recordManualBan(c, target, reason, until)

	if conn, _, ok := c.server.FindPlayer(target); ok {
		_ = conn.sendPlayDisconnect(fmt.Sprintf("You are banned: %s (until %s)",
			reason, until.Format("2006-01-02 15:04:05")))
		go conn.cleanup()
	}
}

func cmdUnban(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /unban <player>")
		return
	}
	if err := c.server.Bans.Remove(context.Background(), args[0]); err != nil {
		slog.Error("ban store remove failed", "target", args[0], "err", err)
		_ = c.sendSystemMessage("Unban failed (storage error): " + err.Error())
		return
	}
	_ = c.sendSystemMessage("Unbanned " + args[0])
}

// cmdKick: /kick <player> [reason words…]
//
// Closes the target's connection with a Play Disconnect packet so the
// vanilla client shows the reason on the disconnect screen. No ban entry
// is created — they can rejoin immediately.
func cmdKick(c *ClientConnection, args []string) {
	if len(args) < 1 {
		_ = c.sendSystemMessage("Usage: /kick <player> [reason]")
		return
	}
	target := args[0]
	reason := "kicked by " + c.playerName
	if len(args) > 1 {
		reason = strings.Join(args[1:], " ")
	}
	conn, _, ok := c.server.FindPlayer(target)
	if !ok {
		_ = c.sendSystemMessage("Player not found: " + target)
		return
	}
	_ = conn.sendPlayDisconnect("Kicked: " + reason)
	_ = c.sendSystemMessage(fmt.Sprintf("Kicked %s — %s", target, reason))
	go conn.cleanup()
}

// cmdMute: /mute <player> <duration>
//
// Suppresses Sb Chat Message broadcasts from the target until expiry.
// Commands (Sb Chat Command) are NOT affected — mods can still see and
// respond to /msg / /report / etc. Adjust the chat handler if you want
// to silence those too.
func cmdMute(c *ClientConnection, args []string) {
	if len(args) != 2 {
		_ = c.sendSystemMessage("Usage: /mute <player> <duration>")
		return
	}
	target := args[0]
	dur, err := moderation.ParseShortDuration(args[1])
	if err != nil {
		_ = c.sendSystemMessage("Bad duration: " + err.Error())
		return
	}
	until := time.Now().Add(dur)
	c.server.Mutes.Mute(target, until)
	_ = c.sendSystemMessage(fmt.Sprintf("Muted %s until %s",
		target, until.Format("2006-01-02 15:04:05")))

	if conn, _, ok := c.server.FindPlayer(target); ok {
		_ = conn.sendSystemMessage(fmt.Sprintf("You have been muted until %s",
			until.Format("2006-01-02 15:04:05")))
	}
}

func cmdUnmute(c *ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.sendSystemMessage("Usage: /unmute <player>")
		return
	}
	c.server.Mutes.Unmute(args[0])
	_ = c.sendSystemMessage("Unmuted " + args[0])
	if conn, _, ok := c.server.FindPlayer(args[0]); ok {
		_ = conn.sendSystemMessage("You have been unmuted")
	}
}
