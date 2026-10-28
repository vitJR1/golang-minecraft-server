package store

import (
	"testing"
	"time"

	"minecraft-server/anticheat"
)

func TestAnticheatRepoRoundTrip(t *testing.T) {
	s, ctx := testStore(t)
	const uuid = "33333333-3333-3333-3333-333333333333"
	repo := s.Anticheat

	if st, err := repo.Status(ctx, uuid); err != nil || st != anticheat.StatusClean {
		t.Fatalf("unknown player: %v %v", st, err)
	}
	t0 := time.Now().UTC().Truncate(time.Microsecond)
	reasons := []anticheat.Violation{{Check: "badpackets.pitch", Detail: "pitch 180.0", At: t0, VL: 3}}
	if err := repo.Record(ctx, anticheat.Record{UUID: uuid, Name: "Cheater", At: t0, Instance: "hub",
		Before: anticheat.StatusClean, After: anticheat.StatusSuspicious, Action: anticheat.ActionKick,
		Reasons: reasons, By: anticheat.IssuedByAnticheat}); err != nil {
		t.Fatal(err)
	}
	until := t0.Add(24 * time.Hour)
	if err := repo.Record(ctx, anticheat.Record{UUID: uuid, Name: "Cheater", At: t0.Add(time.Minute), Instance: "bw-1",
		Before: anticheat.StatusSuspicious, After: anticheat.StatusBanned1d, Action: anticheat.ActionBan,
		BanUntil: until, Reasons: reasons, By: anticheat.IssuedByAnticheat}); err != nil {
		t.Fatal(err)
	}
	if st, _ := repo.Status(ctx, uuid); st != anticheat.StatusBanned1d {
		t.Fatalf("status = %s", st)
	}
	hist, err := repo.History(ctx, uuid, 10)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history: %d, %v", len(hist), err)
	}
	if hist[0].Action != anticheat.ActionBan || !hist[0].BanUntil.Equal(until) || !hist[1].BanUntil.IsZero() {
		t.Fatalf("history: %+v", hist)
	}
	if hist[1].Reasons[0].Detail != "pitch 180.0" || hist[1].Reasons[0].VL != 3 {
		t.Fatalf("reasons: %+v", hist[1].Reasons)
	}
	if u, ok, err := repo.UUIDByName(ctx, "CHEATER"); err != nil || !ok || u != uuid {
		t.Fatalf("UUIDByName = %q %v %v", u, ok, err)
	}
}
