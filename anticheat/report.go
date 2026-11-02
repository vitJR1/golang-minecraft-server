package anticheat

import (
	"fmt"
	"time"
)

// report.go: what a detection and the admins' /ac and /ban actions write —
// the punishment record, the texts the player and the admins see, and the
// /ac info listing. The server applies them (server/anticheat_punish.go).

// Detection is what a punishment needs from the connection, captured on
// the goroutine that flagged it.
type Detection struct {
	Check    string
	UUID     string
	Name     string
	Instance string
	Reasons  []Violation
}

// Punishment is a detection's ladder step spelled out.
type Punishment struct {
	Record Record
	// Reason goes on the ban (when Record.Action bans) and in the kick log:
	// it names the check.
	Reason string
	// Kick is what the player's disconnect screen says. It never names the
	// check — that would help a cheat tune itself.
	Kick string
	// Notice is the ops' chat line ("" unless the step notifies them).
	Notice string
}

// Punish applies Escalate to d at status before.
func Punish(d Detection, before Status, now time.Time) Punishment {
	step := Escalate(before)
	p := Punishment{
		Record: Record{
			UUID: d.UUID, Name: d.Name, At: now, Instance: d.Instance,
			Before: before, After: step.Next, Action: step.Action,
			FlagAdmin: step.NotifyAdmins, Reasons: d.Reasons, By: IssuedByAnticheat,
		},
		Reason: fmt.Sprintf("anticheat: %s (status %s)", d.Check, step.Next),
		Kick:   KickMessage,
	}
	if step.Action == ActionBan {
		p.Record.BanUntil = now.Add(step.Ban)
		p.Kick = fmt.Sprintf("You are banned for suspected cheating (until %s)",
			p.Record.BanUntil.Format("2006-01-02 15:04:05"))
	}
	if step.NotifyAdmins {
		p.Notice = fmt.Sprintf("§c[AC] %s: %s → %s (%s, %s). /ac info %s",
			d.Name, before, step.Next, step.Action, d.Check, d.Name)
	}
	return p
}

// KickMessage is the generic disconnect text of an anticheat kick.
const KickMessage = "Disconnected: suspected cheating"

// AdminRecord is an admin's own entry in the log: by did action with
// reason, moving the player from before to after (until for a ban).
func AdminRecord(uuid, name, instance, by, reason, action string, before, after Status, until, now time.Time) Record {
	return Record{
		UUID: uuid, Name: name, At: now, Instance: instance, Before: before, After: after,
		Action: action, BanUntil: until, By: by,
		Reasons: []Violation{{Check: "manual", Detail: reason, At: now}},
	}
}

// InfoLines is the /ac info listing: the status headline, then each record
// and its reasons.
func InfoLines(name string, status Status, recs []Record) []string {
	lines := []string{fmt.Sprintf("§e%s§r: status §c%s§r (%d), %d record(s) shown",
		name, status, int(status), len(recs))}
	for _, r := range recs {
		line := fmt.Sprintf("§7%s§r %s→%s §f%s§r by %s",
			r.At.Format("2006-01-02 15:04"), r.Before, r.After, r.Action, r.By)
		if !r.BanUntil.IsZero() {
			line += " until " + r.BanUntil.Format("2006-01-02 15:04")
		}
		if r.Instance != "" {
			line += " in " + r.Instance
		}
		lines = append(lines, line)
		for _, v := range r.Reasons {
			lines = append(lines, fmt.Sprintf("   §8- %s: %s (vl %.1f)", v.Check, v.Detail, v.VL))
		}
	}
	return lines
}
