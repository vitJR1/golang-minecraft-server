package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"minecraft-server/anticheat"
)

// anticheat_punish.go turns detections into the punishment ladder of
// anticheat.md: kick → ban 1d → 3d → 7d → max (ban 1d + admins notified) →
// at max every detection kicks and notifies, the ban is the admins' call.
// Each step is logged with its reasons in Server.AnticheatLog. Without
// Server.AnticheatEnforce detections are only written to the server log.

// punishTimeout bounds the store round-trips of one punishment.
const punishTimeout = 5 * time.Second

// detection is what a punishment needs from the connection, captured on the
// goroutine that flagged it (the store I/O then runs on its own goroutine).
type detection struct {
	check    string
	uuid     string
	name     string
	instance string
	reasons  []anticheat.Violation
}

// detect is called by flag when check's level reaches its threshold. Only
// the first detection of a session counts.
func (c *ClientConnection) detect(check string) {
	if c.player == nil || !c.ac.Detected.CompareAndSwap(false, true) {
		return
	}
	d := detection{
		check:   check,
		uuid:    uuidString(c.player.UUID),
		name:    c.playerName,
		reasons: c.ac.Violations.Recent(),
	}
	if c.instance != nil {
		d.instance = c.instance.ID
	}
	if c.server == nil || !c.server.AnticheatEnforce || c.server.AnticheatLog == nil {
		slog.Warn("anticheat detection (log only; ANTICHEAT=enforce punishes)",
			"player", d.name, "check", check, "reasons", len(d.reasons))
		return
	}
	go c.punish(d)
}

// punish applies the ladder step for d: ban (if the step bans), the log
// record, the admin notice, then the kick. A store that can't answer still
// gets the player kicked — the ladder just doesn't move.
func (c *ClientConnection) punish(d detection) {
	s := c.server
	ctx, cancel := context.WithTimeout(context.Background(), punishTimeout)
	defer cancel()

	before, err := s.AnticheatLog.Status(ctx, d.uuid)
	if err != nil {
		slog.Error("anticheat status lookup failed; kicking without escalation",
			"player", d.name, "check", d.check, "err", err)
		c.kickFor("Disconnected: suspected cheating", "anticheat: "+d.check)
		return
	}
	step := anticheat.Escalate(before)
	now := time.Now()
	rec := anticheat.Record{
		UUID: d.uuid, Name: d.name, At: now, Instance: d.instance,
		Before: before, After: step.Next, Action: step.Action,
		FlagAdmin: step.NotifyAdmins, Reasons: d.reasons, By: anticheat.IssuedByAnticheat,
	}
	reason := fmt.Sprintf("anticheat: %s (status %s)", d.check, step.Next)
	// The player only sees a generic message: naming the check helps a
	// cheat tune itself. Admins get the details.
	msg := "Disconnected: suspected cheating"
	if step.Action == anticheat.ActionBan {
		rec.BanUntil = now.Add(step.Ban)
		if err := s.Bans.Add(ctx, d.name, reason, anticheat.IssuedByAnticheat, rec.BanUntil); err != nil {
			slog.Error("anticheat ban write failed", "player", d.name, "err", err)
		}
		msg = fmt.Sprintf("You are banned for suspected cheating (until %s)",
			rec.BanUntil.Format("2006-01-02 15:04:05"))
	}
	if err := s.AnticheatLog.Record(ctx, rec); err != nil {
		slog.Error("anticheat record write failed", "player", d.name, "err", err)
	}
	slog.Warn("anticheat punishment",
		"player", d.name, "check", d.check, "status", fmt.Sprintf("%s→%s", before, step.Next),
		"action", step.Action, "ban_until", rec.BanUntil)
	if step.NotifyAdmins {
		s.notifyOps(fmt.Sprintf("§c[AC] %s: %s → %s (%s, %s). /ac info %s",
			d.name, before, step.Next, step.Action, d.check, d.name))
	}
	c.kickFor(msg, reason)
}

// notifyOps sends msg to every online operator.
func (s *Server) notifyOps(msg string) {
	s.forEachPlayer(func(c *ClientConnection) {
		if s.Ops.Has(c.playerName) {
			_ = c.sendSystemMessage(msg)
		}
	})
}

// forEachPlayer calls fn for every logged-in player in every instance.
func (s *Server) forEachPlayer(fn func(c *ClientConnection)) {
	s.mu.RLock()
	instances := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		instances = append(instances, i)
	}
	s.mu.RUnlock()
	for _, i := range instances {
		i.Players.Range(fn)
	}
}

// acTarget resolves a player name for /ac: an online player's UUID, else
// the UUID on their latest punishment record.
func (s *Server) acTarget(ctx context.Context, name string) (uuid, display string, ok bool, err error) {
	if conn, _, online := s.FindPlayer(name); online && conn.player != nil {
		return uuidString(conn.player.UUID), conn.playerName, true, nil
	}
	uuid, ok, err = s.AnticheatLog.UUIDByName(ctx, name)
	return uuid, name, ok, err
}

// cmdAnticheat: /ac info <player> | /ac reset <player> [reason…]
func cmdAnticheat(c *ClientConnection, args []string) {
	usage := "Usage: /ac info <player>  |  /ac reset <player> [reason]"
	if len(args) < 2 {
		_ = c.sendSystemMessage(usage)
		return
	}
	s := c.server
	ctx, cancel := context.WithTimeout(context.Background(), punishTimeout)
	defer cancel()
	uuid, name, ok, err := s.acTarget(ctx, args[1])
	switch {
	case err != nil:
		_ = c.sendSystemMessage("Anticheat store error: " + err.Error())
		return
	case !ok:
		_ = c.sendSystemMessage(args[1] + " is offline and has no anticheat record")
		return
	}
	switch strings.ToLower(args[0]) {
	case "info":
		acInfo(ctx, c, uuid, name)
	case "reset":
		reason := "reset by " + c.playerName
		if len(args) > 2 {
			reason = strings.Join(args[2:], " ")
		}
		acReset(ctx, c, uuid, name, reason)
	default:
		_ = c.sendSystemMessage(usage)
	}
}

// acInfoLimit is how many records /ac info lists.
const acInfoLimit = 10

func acInfo(ctx context.Context, c *ClientConnection, uuid, name string) {
	s := c.server
	status, err := s.AnticheatLog.Status(ctx, uuid)
	if err != nil {
		_ = c.sendSystemMessage("Anticheat store error: " + err.Error())
		return
	}
	recs, err := s.AnticheatLog.History(ctx, uuid, acInfoLimit)
	if err != nil {
		_ = c.sendSystemMessage("Anticheat store error: " + err.Error())
		return
	}
	_ = c.sendSystemMessage(fmt.Sprintf("§e%s§r: status §c%s§r (%d), %d record(s) shown",
		name, status, int(status), len(recs)))
	for _, r := range recs {
		line := fmt.Sprintf("§7%s§r %s→%s §f%s§r by %s",
			r.At.Format("2006-01-02 15:04"), r.Before, r.After, r.Action, r.By)
		if !r.BanUntil.IsZero() {
			line += " until " + r.BanUntil.Format("2006-01-02 15:04")
		}
		if r.Instance != "" {
			line += " in " + r.Instance
		}
		_ = c.sendSystemMessage(line)
		for _, v := range r.Reasons {
			_ = c.sendSystemMessage(fmt.Sprintf("   §8- %s: %s (vl %.1f)", v.Check, v.Detail, v.VL))
		}
	}
}

func acReset(ctx context.Context, c *ClientConnection, uuid, name, reason string) {
	s := c.server
	before, err := s.AnticheatLog.Status(ctx, uuid)
	if err != nil {
		_ = c.sendSystemMessage("Anticheat store error: " + err.Error())
		return
	}
	rec := anticheat.Record{
		UUID: uuid, Name: name, At: time.Now(), Before: before, After: anticheat.StatusClean,
		Action: anticheat.ActionReset, By: c.playerName,
		Reasons: []anticheat.Violation{{Check: "manual", Detail: reason, At: time.Now()}},
	}
	if c.instance != nil {
		rec.Instance = c.instance.ID
	}
	if err := s.AnticheatLog.Record(ctx, rec); err != nil {
		_ = c.sendSystemMessage("Anticheat store error: " + err.Error())
		return
	}
	_ = c.sendSystemMessage(fmt.Sprintf("%s: status %s → clean", name, before))
}

// recordManualBan logs an admin's /ban of a player at the max status — the
// ban the ladder leaves to the admins — with the admin's reason.
func (s *Server) recordManualBan(admin *ClientConnection, target, reason string, until time.Time) {
	if s.AnticheatLog == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), punishTimeout)
	defer cancel()
	uuid, name, ok, err := s.acTarget(ctx, target)
	if err != nil || !ok {
		return
	}
	status, err := s.AnticheatLog.Status(ctx, uuid)
	if err != nil || status != anticheat.StatusMax {
		return
	}
	rec := anticheat.Record{
		UUID: uuid, Name: name, At: time.Now(), Before: status, After: status,
		Action: anticheat.ActionBan, BanUntil: until, By: admin.playerName,
		Reasons: []anticheat.Violation{{Check: "manual", Detail: reason, At: time.Now()}},
	}
	if admin.instance != nil {
		rec.Instance = admin.instance.ID
	}
	if err := s.AnticheatLog.Record(ctx, rec); err != nil {
		slog.Error("anticheat manual ban record failed", "target", target, "err", err)
	}
}
