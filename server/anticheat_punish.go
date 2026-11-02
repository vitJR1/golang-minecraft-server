package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/protocol"
)

// anticheat_punish.go turns detections into the punishment ladder of
// anticheat.md: kick → ban 1d → 3d → 7d → max (ban 1d + admins notified) →
// at max every detection kicks and notifies, the ban is the admins' call.
// Each step is logged with its reasons in Server.AnticheatLog. Without
// Server.AnticheatEnforce detections are only written to the server log.

// punishTimeout bounds the store round-trips of one punishment.
const punishTimeout = 5 * time.Second

// detect is called by flag when check's level reaches its threshold. Only
// the first detection of a session counts.
func (c *ClientConnection) detect(check string) {
	if c.player == nil || !c.ac.Detected.CompareAndSwap(false, true) {
		return
	}
	d := anticheat.Detection{
		Check:   check,
		UUID:    protocol.UUIDString(c.player.UUID),
		Name:    c.playerName,
		Reasons: c.ac.Violations.Recent(),
	}
	if c.instance != nil {
		d.Instance = c.instance.ID
	}
	if c.server == nil || !c.server.AnticheatEnforce || c.server.AnticheatLog == nil {
		slog.Warn("anticheat detection (log only; ANTICHEAT=enforce punishes)",
			"player", d.Name, "check", check, "reasons", len(d.Reasons))
		return
	}
	go c.punish(d)
}

// punish applies the ladder step for d (anticheat.Punish): ban (if the step
// bans), the log record, the admin notice, then the kick. A store that can't
// answer still gets the player kicked — the ladder just doesn't move.
func (c *ClientConnection) punish(d anticheat.Detection) {
	s := c.server
	ctx, cancel := context.WithTimeout(context.Background(), punishTimeout)
	defer cancel()

	before, err := s.AnticheatLog.Status(ctx, d.UUID)
	if err != nil {
		slog.Error("anticheat status lookup failed; kicking without escalation",
			"player", d.Name, "check", d.Check, "err", err)
		c.kickFor(anticheat.KickMessage, "anticheat: "+d.Check)
		return
	}
	p := anticheat.Punish(d, before, time.Now())
	rec := p.Record
	if rec.Action == anticheat.ActionBan {
		if err := s.Bans.Add(ctx, d.Name, p.Reason, anticheat.IssuedByAnticheat, rec.BanUntil); err != nil {
			slog.Error("anticheat ban write failed", "player", d.Name, "err", err)
		}
	}
	if err := s.AnticheatLog.Record(ctx, rec); err != nil {
		slog.Error("anticheat record write failed", "player", d.Name, "err", err)
	}
	slog.Warn("anticheat punishment",
		"player", d.Name, "check", d.Check, "status", fmt.Sprintf("%s→%s", before, rec.After),
		"action", rec.Action, "ban_until", rec.BanUntil)
	if p.Notice != "" {
		s.notifyOps(p.Notice)
	}
	c.kickFor(p.Kick, p.Reason)
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
		return protocol.UUIDString(conn.player.UUID), conn.playerName, true, nil
	}
	uuid, ok, err = s.AnticheatLog.UUIDByName(ctx, name)
	return uuid, name, ok, err
}

func init() {
	registerCommand(&Command{
		Name:    "ac",
		NeedsOp: true,
		Help:    "/ac info <player> | /ac reset <player> [reason] — anticheat status + punishment log",
		Run:     cmdAnticheat,
	})
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
	for _, line := range anticheat.InfoLines(name, status, recs) {
		_ = c.sendSystemMessage(line)
	}
}

func acReset(ctx context.Context, c *ClientConnection, uuid, name, reason string) {
	s := c.server
	before, err := s.AnticheatLog.Status(ctx, uuid)
	if err != nil {
		_ = c.sendSystemMessage("Anticheat store error: " + err.Error())
		return
	}
	rec := anticheat.AdminRecord(uuid, name, c.instanceID(), c.playerName, reason,
		anticheat.ActionReset, before, anticheat.StatusClean, time.Time{}, time.Now())
	if err := s.AnticheatLog.Record(ctx, rec); err != nil {
		_ = c.sendSystemMessage("Anticheat store error: " + err.Error())
		return
	}
	_ = c.sendSystemMessage(fmt.Sprintf("%s: status %s → clean", name, before))
}

// RecordManualBan logs an admin's /ban of a player at the max status — the
// ban the ladder leaves to the admins — with the admin's reason.
func (s *Server) RecordManualBan(admin *ClientConnection, target, reason string, until time.Time) {
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
	rec := anticheat.AdminRecord(uuid, name, admin.instanceID(), admin.playerName, reason,
		anticheat.ActionBan, status, status, until, time.Now())
	if err := s.AnticheatLog.Record(ctx, rec); err != nil {
		slog.Error("anticheat manual ban record failed", "target", target, "err", err)
	}
}

// instanceID is the id of the instance c is in ("" when none).
func (c *ClientConnection) instanceID() string {
	if c.instance == nil {
		return ""
	}
	return c.instance.ID
}
