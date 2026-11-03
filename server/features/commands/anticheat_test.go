package commands

import (
	"context"
	"testing"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server"
	"minecraft-server/server/net/servertest"
)

// setStatus records a past punishment that left name's player at st.
func setStatus(t *testing.T, s *server.Server, name string, st anticheat.Status) string {
	t.Helper()
	uuid := protocol.OfflineUUID(name)
	err := s.AnticheatLog.Record(context.Background(), anticheat.Record{
		UUID: uuid, Name: name, At: time.Now(), After: st, Action: anticheat.ActionKick, By: "test",
		Reasons: []anticheat.Violation{{Check: "badpackets.pitch", Detail: "pitch 180.0", VL: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return uuid
}

func TestAcResetClearsStatusAndLogsAdmin(t *testing.T) {
	s := server.New()
	s.Ops.Add("Admin")
	_, _ = login(t, s, "Reformed")
	uuid := setStatus(t, s, "Reformed", anticheat.StatusBanned3d)
	_, admin := login(t, s, "Admin")

	s.RunCommand(admin, "ac reset Reformed appealed on discord")
	if st, _ := s.AnticheatLog.Status(context.Background(), uuid); st != anticheat.StatusClean {
		t.Fatalf("status after reset = %s", st)
	}
	recs, err := s.AnticheatLog.History(context.Background(), uuid, 1)
	if err != nil || len(recs) == 0 {
		t.Fatalf("no record: %v", err)
	}
	rec := recs[0]
	if rec.Action != anticheat.ActionReset || rec.By != "Admin" || rec.Before != anticheat.StatusBanned3d ||
		len(rec.Reasons) != 1 || rec.Reasons[0].Detail != "appealed on discord" || rec.Instance != "hub" {
		t.Fatalf("reset record = %+v", rec)
	}
}

func TestAcInfoListsRecords(t *testing.T) {
	s := server.New()
	s.Ops.Add("Admin")
	setStatus(t, s, "Shown", anticheat.StatusSuspicious) // offline: found by the record's name
	cli := servertest.Dial(t, s)
	cli.Login(t, "Admin")
	ids := cli.StartDrain()
	admin := findConn(t, s, "Admin")

	s.RunCommand(admin, "ac info Shown")
	chats := 0
	deadline := time.After(2 * time.Second)
	for chats < 3 { // header + record + its reason
		select {
		case id := <-ids:
			if id == packetid.CbPlaySystemChat {
				chats++
			}
		case <-deadline:
			t.Fatalf("/ac info sent %d messages", chats)
		}
	}
}

func TestAcNeedsOp(t *testing.T) {
	s := server.New()
	_, plain := login(t, s, "Nosy")
	uuid := setStatus(t, s, "Shown", anticheat.StatusSuspicious)
	s.RunCommand(plain, "ac reset Shown")
	if st, _ := s.AnticheatLog.Status(context.Background(), uuid); st != anticheat.StatusSuspicious {
		t.Fatal("a non-op reset the status")
	}
}
