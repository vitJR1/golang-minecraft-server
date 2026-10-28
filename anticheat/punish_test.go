package anticheat

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestEscalateLadder(t *testing.T) {
	const day = 24 * time.Hour
	cases := []struct {
		at     Status
		want   Step
		reason string
	}{
		{StatusClean, Step{Next: StatusSuspicious, Action: ActionKick}, "first detection kicks"},
		{StatusSuspicious, Step{Next: StatusBanned1d, Action: ActionBan, Ban: day}, "then 1 day"},
		{StatusBanned1d, Step{Next: StatusBanned3d, Action: ActionBan, Ban: 3 * day}, "then 3 days"},
		{StatusBanned3d, Step{Next: StatusBanned7d, Action: ActionBan, Ban: 7 * day}, "then a week"},
		{StatusBanned7d, Step{Next: StatusMax, Action: ActionBan, Ban: day, NotifyAdmins: true}, "max: 1 day + admins"},
		{StatusMax, Step{Next: StatusMax, Action: ActionKick, NotifyAdmins: true}, "at max: kick + admins"},
	}
	for _, tc := range cases {
		if got := Escalate(tc.at); got != tc.want {
			t.Errorf("%s: Escalate(%s) = %+v, want %+v", tc.reason, tc.at, got, tc.want)
		}
	}
}

func TestThresholdOverride(t *testing.T) {
	if Threshold("badpackets.pitch") != DefaultThreshold {
		t.Fatal("default threshold not used")
	}
	Thresholds["reach"] = 10
	t.Cleanup(func() { delete(Thresholds, "reach") })
	if Threshold("reach") != 10 {
		t.Fatal("override ignored")
	}
}

// storeContract runs the Store behaviour every backend must share.
func storeContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	const uuid = "11111111-1111-1111-1111-111111111111"
	if st, err := s.Status(ctx, uuid); err != nil || st != StatusClean {
		t.Fatalf("unknown player: %v %v", st, err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	reason := []Violation{{Check: "badpackets.pitch", Detail: "pitch 180.0", At: t0, VL: 3}}
	if err := s.Record(ctx, Record{UUID: uuid, Name: "Cheater", At: t0, Instance: "hub",
		Before: StatusClean, After: StatusSuspicious, Action: ActionKick, Reasons: reason, By: IssuedByAnticheat}); err != nil {
		t.Fatal(err)
	}
	until := t0.Add(48 * time.Hour)
	if err := s.Record(ctx, Record{UUID: uuid, Name: "Cheater", At: t0.Add(time.Hour), Instance: "bw-1",
		Before: StatusSuspicious, After: StatusBanned1d, Action: ActionBan, BanUntil: until, Reasons: reason, By: IssuedByAnticheat}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status(ctx, uuid); st != StatusBanned1d {
		t.Fatalf("status = %s, want banned_1d", st)
	}
	hist, err := s.History(ctx, uuid, 10)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history: %d records, %v", len(hist), err)
	}
	if hist[0].Action != ActionBan || !hist[0].BanUntil.Equal(until) || hist[1].Action != ActionKick {
		t.Fatalf("history not newest first: %+v", hist)
	}
	if len(hist[1].Reasons) != 1 || hist[1].Reasons[0].Detail != "pitch 180.0" {
		t.Fatalf("reasons lost: %+v", hist[1].Reasons)
	}
	if got, _ := s.History(ctx, uuid, 1); len(got) != 1 {
		t.Fatalf("limit ignored: %d", len(got))
	}
	if u, ok, err := s.UUIDByName(ctx, "cheater"); err != nil || !ok || u != uuid {
		t.Fatalf("UUIDByName = %q %v %v", u, ok, err)
	}
	if _, ok, _ := s.UUIDByName(ctx, "nobody"); ok {
		t.Fatal("unknown name resolved")
	}
}

func TestMemoryStoreContract(t *testing.T) { storeContract(t, NewMemoryStore()) }

func TestFileStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anticheat.json")
	fs, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	storeContract(t, fs)

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const uuid = "11111111-1111-1111-1111-111111111111"
	if st, _ := reloaded.Status(ctx, uuid); st != StatusBanned1d {
		t.Fatalf("reloaded status = %s", st)
	}
	hist, _ := reloaded.History(ctx, uuid, 0)
	if len(hist) != 2 || hist[0].Reasons[0].Check != "badpackets.pitch" {
		t.Fatalf("reloaded history: %+v", hist)
	}
	// New records continue the id sequence.
	if err := reloaded.Record(ctx, Record{UUID: uuid, Name: "Cheater", After: StatusClean, Action: ActionReset}); err != nil {
		t.Fatal(err)
	}
	if h, _ := reloaded.History(ctx, uuid, 1); h[0].ID != 3 {
		t.Fatalf("id after reload = %d, want 3", h[0].ID)
	}
}
