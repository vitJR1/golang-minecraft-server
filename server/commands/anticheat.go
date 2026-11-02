package commands

import (
	"context"
	"fmt"
	"minecraft-server/anticheat"
	"minecraft-server/server"
	"strings"
	"time"
)

// anticheat.go: /ac info|reset — the punishment log (anticheat.Store on
// Server.AnticheatLog).

// storeTimeout bounds the store round-trips of one /ac.
const storeTimeout = 5 * time.Second

// instanceID is the id of the instance c is in ("" when none).
func instanceID(c *server.ClientConnection) string {
	if c.Instance() == nil {
		return ""
	}
	return c.Instance().ID
}

func init() {
	server.RegisterCommand(&server.Command{
		Name:    "ac",
		NeedsOp: true,
		Help:    "/ac info <player> | /ac reset <player> [reason] — anticheat status + punishment log",
		Run:     cmdAnticheat,
	})
}

// cmdAnticheat: /ac info <player> | /ac reset <player> [reason…]
func cmdAnticheat(c *server.ClientConnection, args []string) {
	usage := "Usage: /ac info <player>  |  /ac reset <player> [reason]"
	if len(args) < 2 {
		_ = c.SendMessage(usage)
		return
	}
	s := c.Server()
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	uuid, name, ok, err := s.AnticheatTarget(ctx, args[1])
	switch {
	case err != nil:
		_ = c.SendMessage("Anticheat store error: " + err.Error())
		return
	case !ok:
		_ = c.SendMessage(args[1] + " is offline and has no anticheat record")
		return
	}
	switch strings.ToLower(args[0]) {
	case "info":
		acInfo(ctx, c, uuid, name)
	case "reset":
		reason := "reset by " + c.Name()
		if len(args) > 2 {
			reason = strings.Join(args[2:], " ")
		}
		acReset(ctx, c, uuid, name, reason)
	default:
		_ = c.SendMessage(usage)
	}
}

// acInfoLimit is how many records /ac info lists.
const acInfoLimit = 10

func acInfo(ctx context.Context, c *server.ClientConnection, uuid, name string) {
	s := c.Server()
	status, err := s.AnticheatLog.Status(ctx, uuid)
	if err != nil {
		_ = c.SendMessage("Anticheat store error: " + err.Error())
		return
	}
	recs, err := s.AnticheatLog.History(ctx, uuid, acInfoLimit)
	if err != nil {
		_ = c.SendMessage("Anticheat store error: " + err.Error())
		return
	}
	for _, line := range anticheat.InfoLines(name, status, recs) {
		_ = c.SendMessage(line)
	}
}

func acReset(ctx context.Context, c *server.ClientConnection, uuid, name, reason string) {
	s := c.Server()
	before, err := s.AnticheatLog.Status(ctx, uuid)
	if err != nil {
		_ = c.SendMessage("Anticheat store error: " + err.Error())
		return
	}
	rec := anticheat.AdminRecord(uuid, name, instanceID(c), c.Name(), reason,
		anticheat.ActionReset, before, anticheat.StatusClean, time.Time{}, time.Now())
	if err := s.AnticheatLog.Record(ctx, rec); err != nil {
		_ = c.SendMessage("Anticheat store error: " + err.Error())
		return
	}
	_ = c.SendMessage(fmt.Sprintf("%s: status %s → clean", name, before))
}
