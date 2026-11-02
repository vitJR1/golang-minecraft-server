package anticheat

import (
	"strings"
	"testing"
	"time"
)

func TestPunishSteps(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	d := Detection{Check: "movement.fly", UUID: "u", Name: "Cheater", Instance: "hub",
		Reasons: []Violation{{Check: "movement.fly", Detail: "hover"}}}

	p := Punish(d, StatusClean, now)
	if p.Record.Action != ActionKick || p.Record.After != StatusSuspicious || !p.Record.BanUntil.IsZero() ||
		p.Kick != KickMessage || p.Notice != "" || p.Record.By != IssuedByAnticheat || len(p.Record.Reasons) != 1 {
		t.Errorf("clean → %+v", p)
	}
	if strings.Contains(p.Kick, "fly") || !strings.Contains(p.Reason, "movement.fly") {
		t.Errorf("the check goes in Reason, never in Kick: %q / %q", p.Kick, p.Reason)
	}

	p = Punish(d, StatusBanned3d, now)
	if p.Record.Action != ActionBan || !p.Record.BanUntil.Equal(now.Add(7*24*time.Hour)) ||
		!strings.Contains(p.Kick, "2026-01-09 03:04:05") {
		t.Errorf("banned_3d → %+v", p)
	}

	p = Punish(d, StatusMax, now)
	if p.Record.Action != ActionKick || !p.Record.FlagAdmin || !strings.Contains(p.Notice, "/ac info Cheater") {
		t.Errorf("max → %+v", p)
	}
}

func TestAdminRecordAndInfoLines(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	r := AdminRecord("u", "P", "hub", "Admin", "appeal", ActionReset, StatusBanned1d, StatusClean, time.Time{}, now)
	if r.By != "Admin" || r.Reasons[0].Check != "manual" || r.Reasons[0].Detail != "appeal" || r.After != StatusClean {
		t.Errorf("record %+v", r)
	}
	ban := Record{At: now, Before: StatusMax, After: StatusMax, Action: ActionBan, By: "Admin",
		BanUntil: now.Add(time.Hour), Reasons: []Violation{{Check: "manual", Detail: "x", VL: 0}}}
	lines := InfoLines("P", StatusMax, []Record{r, ban})
	if len(lines) != 5 || !strings.Contains(lines[0], "max") || !strings.Contains(lines[1], " in hub") ||
		!strings.Contains(lines[3], "until 2026-01-02 04:04") || !strings.HasPrefix(lines[4], "   §8- manual: x") {
		t.Errorf("lines %q", lines)
	}
}
