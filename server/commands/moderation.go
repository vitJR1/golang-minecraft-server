package commands

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"minecraft-server/server"
	"minecraft-server/server/moderation"
)

// cmd_moderation.go: /ban /unban /kick /mute /unmute.

func init() {
	server.RegisterCommand(&server.Command{
		Name:     "ban",
		NeedsOp:  true,
		Help:     "/ban <player> <duration> [reason] — 1m, 7d, 2h, 1w",
		Run:      cmdBan,
		Complete: playerArg(1),
	})
	server.RegisterCommand(&server.Command{
		Name:    "unban",
		NeedsOp: true,
		Help:    "/unban <player>",
		Run:     cmdUnban,
	})
	server.RegisterCommand(&server.Command{
		Name:     "kick",
		NeedsOp:  true,
		Help:     "/kick <player> [reason]",
		Run:      cmdKick,
		Complete: playerArg(1),
	})
	server.RegisterCommand(&server.Command{
		Name:     "mute",
		NeedsOp:  true,
		Help:     "/mute <player> <duration> — 30s, 5m, 1h, 7d",
		Run:      cmdMute,
		Complete: playerArg(1),
	})
	server.RegisterCommand(&server.Command{
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
func cmdBan(c *server.ClientConnection, args []string) {
	if len(args) < 2 {
		_ = c.SendMessage("Usage: /ban <player> <duration> [reason]")
		return
	}
	target := args[0]
	dur, err := moderation.ParseShortDuration(args[1])
	if err != nil {
		_ = c.SendMessage("Bad duration: " + err.Error())
		return
	}
	reason := "banned by " + c.Name()
	if len(args) > 2 {
		reason = strings.Join(args[2:], " ")
	}
	until := time.Now().Add(dur)
	if err := c.Server().Bans.Add(context.Background(), target, reason, c.Name(), until); err != nil {
		slog.Error("ban store write failed", "target", target, "err", err)
		_ = c.SendMessage("Ban failed (storage error): " + err.Error())
		return
	}

	_ = c.SendMessage(fmt.Sprintf("Banned %s until %s — %s",
		target, until.Format("2006-01-02 15:04:05"), reason))
	// A ban of a player at the anticheat max status is the admins' half of
	// the ladder: log it with the admin's reason.
	c.Server().RecordManualBan(c, target, reason, until)

	if conn, _, ok := c.Server().FindPlayer(target); ok {
		conn.Kick(fmt.Sprintf("You are banned: %s (until %s)",
			reason, until.Format("2006-01-02 15:04:05")))
	}
}

func cmdUnban(c *server.ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.SendMessage("Usage: /unban <player>")
		return
	}
	if err := c.Server().Bans.Remove(context.Background(), args[0]); err != nil {
		slog.Error("ban store remove failed", "target", args[0], "err", err)
		_ = c.SendMessage("Unban failed (storage error): " + err.Error())
		return
	}
	_ = c.SendMessage("Unbanned " + args[0])
}

// cmdKick: /kick <player> [reason words…]
//
// Closes the target's connection with a Play Disconnect packet so the
// vanilla client shows the reason on the disconnect screen. No ban entry
// is created — they can rejoin immediately.
func cmdKick(c *server.ClientConnection, args []string) {
	if len(args) < 1 {
		_ = c.SendMessage("Usage: /kick <player> [reason]")
		return
	}
	target := args[0]
	reason := "kicked by " + c.Name()
	if len(args) > 1 {
		reason = strings.Join(args[1:], " ")
	}
	conn, _, ok := c.Server().FindPlayer(target)
	if !ok {
		_ = c.SendMessage("Player not found: " + target)
		return
	}
	_ = c.SendMessage(fmt.Sprintf("Kicked %s — %s", target, reason))
	conn.Kick("Kicked: " + reason)
}

// cmdMute: /mute <player> <duration>
//
// Suppresses Sb Chat Message broadcasts from the target until expiry.
// Commands (Sb Chat Command) are NOT affected — mods can still see and
// respond to /msg / /report / etc. Adjust the chat handler if you want
// to silence those too.
func cmdMute(c *server.ClientConnection, args []string) {
	if len(args) != 2 {
		_ = c.SendMessage("Usage: /mute <player> <duration>")
		return
	}
	target := args[0]
	dur, err := moderation.ParseShortDuration(args[1])
	if err != nil {
		_ = c.SendMessage("Bad duration: " + err.Error())
		return
	}
	until := time.Now().Add(dur)
	c.Server().Mutes.Mute(target, until)
	_ = c.SendMessage(fmt.Sprintf("Muted %s until %s",
		target, until.Format("2006-01-02 15:04:05")))

	if conn, _, ok := c.Server().FindPlayer(target); ok {
		_ = conn.SendMessage(fmt.Sprintf("You have been muted until %s",
			until.Format("2006-01-02 15:04:05")))
	}
}

func cmdUnmute(c *server.ClientConnection, args []string) {
	if len(args) != 1 {
		_ = c.SendMessage("Usage: /unmute <player>")
		return
	}
	c.Server().Mutes.Unmute(args[0])
	_ = c.SendMessage("Unmuted " + args[0])
	if conn, _, ok := c.Server().FindPlayer(args[0]); ok {
		_ = conn.SendMessage("You have been unmuted")
	}
}
