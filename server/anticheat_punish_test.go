package server

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/protocol"
)

// anticheat_punish_test.go: detections walk the punishment ladder.

// flagsToDetect is how many quick flags of one check make a detection.
const flagsToDetect = 3

// enforcing is a fresh server that punishes detections.
func enforcing() *Server {
	s := New()
	s.AnticheatEnforce = true
	return s
}

// joinOn logs name into s with a draining client.
func joinOn(t *testing.T, s *Server, name string) (*testClient, *ClientConnection) {
	t.Helper()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, name)
	cli.startDiscardDrain()
	return cli, findConn(t, s, name)
}

// cheat sends n Set Rotation packets with an impossible pitch (one
// badpackets.pitch flag each). tryWrite: the connection may get kicked.
func cheat(cli *testClient, n int) {
	var p bytes.Buffer
	p.Write(protocol.WriteFloat(0))
	p.Write(protocol.WriteFloat(180))
	p.WriteByte(1)
	for i := 0; i < n; i++ {
		cli.tryWrite(SbPlaySetRot, p.Bytes())
	}
}

// setStatus puts conn's player at status st in s's anticheat log.
func setStatus(t *testing.T, s *Server, conn *ClientConnection, st anticheat.Status) {
	t.Helper()
	setStatusFor(t, s, uuidString(conn.player.UUID), conn.playerName, st)
}

// setStatusFor records a past punishment that left uuid at st.
func setStatusFor(t *testing.T, s *Server, uuid, name string, st anticheat.Status) {
	t.Helper()
	err := s.AnticheatLog.Record(context.Background(), anticheat.Record{
		UUID: uuid, Name: name, At: time.Now(), After: st, Action: anticheat.ActionKick, By: "test",
		Reasons: []anticheat.Violation{{Check: "badpackets.pitch", Detail: "pitch 180.0", VL: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// latest returns the newest anticheat record of conn's player.
func latest(t *testing.T, s *Server, conn *ClientConnection) anticheat.Record {
	t.Helper()
	return latestFor(t, s, uuidString(conn.player.UUID))
}

func latestFor(t *testing.T, s *Server, uuid string) anticheat.Record {
	t.Helper()
	recs, err := s.AnticheatLog.History(context.Background(), uuid, 1)
	if err != nil || len(recs) == 0 {
		t.Fatalf("no record: %v", err)
	}
	return recs[0]
}

func TestFirstDetectionKicksAndRecordsReasons(t *testing.T) {
	s := enforcing()
	cli, conn := joinOn(t, s, "Spinner")
	cheat(cli, flagsToDetect)
	waitFor(t, 2*time.Second, conn.isClosed, "detected player to be kicked")

	rec := latest(t, s, conn)
	if rec.Before != anticheat.StatusClean || rec.After != anticheat.StatusSuspicious || rec.Action != anticheat.ActionKick {
		t.Fatalf("record = %+v, want clean→suspicious kick", rec)
	}
	if rec.By != anticheat.IssuedByAnticheat || rec.Instance != "hub" || rec.Name != "Spinner" {
		t.Fatalf("record metadata = %+v", rec)
	}
	if len(rec.Reasons) < flagsToDetect || rec.Reasons[0].Check != "badpackets.pitch" {
		t.Fatalf("reasons = %+v", rec.Reasons)
	}
	if info, _ := s.Bans.IsBanned(context.Background(), "Spinner"); info != nil {
		t.Fatal("a first detection must only kick")
	}
}

func TestSecondDetectionBansOneDay(t *testing.T) {
	s := enforcing()
	cli, conn := joinOn(t, s, "Repeat")
	setStatus(t, s, conn, anticheat.StatusSuspicious) // kicked once before
	cheat(cli, flagsToDetect)
	waitFor(t, 2*time.Second, conn.isClosed, "ban kick")

	rec := latest(t, s, conn)
	if rec.After != anticheat.StatusBanned1d || rec.Action != anticheat.ActionBan {
		t.Fatalf("record = %+v", rec)
	}
	info, err := s.Bans.IsBanned(context.Background(), "Repeat")
	if err != nil || info == nil {
		t.Fatalf("not banned: %v", err)
	}
	if d := time.Until(info.ExpiresAt); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("ban length %v, want 1 day", d)
	}
	if !info.ExpiresAt.Equal(rec.BanUntil) || !strings.Contains(info.Reason, "badpackets.pitch") {
		t.Fatalf("ban %+v vs record %+v", info, rec)
	}
}

func TestMaxStatusBansAndNotifiesAdmins(t *testing.T) {
	s := enforcing()
	s.Ops.Add("Admin")
	adminCli := pipeClientOn(t, s)
	completeOfflineLogin(t, adminCli, "Admin")
	adminMsgs := adminCli.startDrain()

	cli, conn := joinOn(t, s, "Hopeless")
	setStatus(t, s, conn, anticheat.StatusBanned7d)
	cheat(cli, flagsToDetect)
	waitFor(t, 2*time.Second, conn.isClosed, "ban kick")

	rec := latest(t, s, conn)
	if rec.After != anticheat.StatusMax || rec.Action != anticheat.ActionBan || !rec.FlagAdmin {
		t.Fatalf("record = %+v, want →max ban flagged for admins", rec)
	}
	if d := time.Until(rec.BanUntil); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("max ban length %v, want 1 day", d)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case id := <-adminMsgs:
			if id == CbPlaySystemChat {
				return // the admin got a notice
			}
		case <-deadline:
			t.Fatal("online admin was not notified")
		}
	}
}

func TestAtMaxDetectionOnlyKicks(t *testing.T) {
	s := enforcing()
	cli, conn := joinOn(t, s, "Watched")
	setStatus(t, s, conn, anticheat.StatusMax)
	cheat(cli, flagsToDetect)
	waitFor(t, 2*time.Second, conn.isClosed, "kick")

	rec := latest(t, s, conn)
	if rec.After != anticheat.StatusMax || rec.Action != anticheat.ActionKick || !rec.FlagAdmin {
		t.Fatalf("record = %+v, want max kick flagged", rec)
	}
	if info, _ := s.Bans.IsBanned(context.Background(), "Watched"); info != nil {
		t.Fatal("at max the ban is the admins' call")
	}
}

func TestOneStepPerSession(t *testing.T) {
	s := enforcing()
	cli, conn := joinOn(t, s, "Burst")
	cheat(cli, 4*flagsToDetect)
	waitFor(t, 2*time.Second, conn.isClosed, "kick")
	time.Sleep(50 * time.Millisecond) // any stray second punishment would land by now
	recs, _ := s.AnticheatLog.History(context.Background(), uuidString(conn.player.UUID), 0)
	if len(recs) != 1 {
		t.Fatalf("%d records for one session, want 1", len(recs))
	}
}

func TestLogModeDoesNotPunish(t *testing.T) {
	s := New() // AnticheatEnforce off
	cli, conn := joinOn(t, s, "Logged")
	cheat(cli, 2*flagsToDetect)
	barrier(t, cli, conn)
	if conn.isClosed() {
		t.Fatal("log mode kicked")
	}
	if recs, _ := s.AnticheatLog.History(context.Background(), uuidString(conn.player.UUID), 0); len(recs) != 0 {
		t.Fatalf("log mode recorded %d punishments", len(recs))
	}
	if !conn.ac.Detected.Load() {
		t.Fatal("the detection itself should still happen (and be logged)")
	}
}

// --- /ac and manual bans ------------------------------------------------

func opConn(t *testing.T, s *Server, name string) *ClientConnection {
	t.Helper()
	s.Ops.Add(name)
	c := offlineConn(s.Hub, name, player.Creative, 0.5, 67, 0.5)
	return c
}

func TestAcResetClearsStatusAndLogsAdmin(t *testing.T) {
	s := New()
	target := offlineConn(s.Hub, "Reformed", player.Survival, 0, 67, 0)
	setStatus(t, s, target, anticheat.StatusBanned3d)
	admin := opConn(t, s, "Admin")

	s.RunCommand(admin, "ac reset Reformed appealed on discord")
	drain(admin)
	uuid := uuidString(target.player.UUID)
	if st, _ := s.AnticheatLog.Status(context.Background(), uuid); st != anticheat.StatusClean {
		t.Fatalf("status after reset = %s", st)
	}
	rec := latest(t, s, target)
	if rec.Action != anticheat.ActionReset || rec.By != "Admin" || rec.Before != anticheat.StatusBanned3d ||
		len(rec.Reasons) != 1 || rec.Reasons[0].Detail != "appealed on discord" {
		t.Fatalf("reset record = %+v", rec)
	}
}

func TestAcInfoListsRecords(t *testing.T) {
	s := New()
	target := offlineConn(s.Hub, "Shown", player.Survival, 0, 67, 0)
	setStatus(t, s, target, anticheat.StatusSuspicious)
	admin := opConn(t, s, "Admin")
	drain(admin)

	s.RunCommand(admin, "ac info Shown")
	if n := len(admin.outbound); n < 3 { // header + record + its reason
		t.Fatalf("/ac info sent %d messages", n)
	}
}

func TestAcInfoNeedsOp(t *testing.T) {
	s := New()
	plain := offlineConn(s.Hub, "Nosy", player.Survival, 0, 67, 0)
	target := offlineConn(s.Hub, "Shown", player.Survival, 0, 67, 0)
	setStatus(t, s, target, anticheat.StatusSuspicious)
	s.RunCommand(plain, "ac reset Shown")
	if st, _ := s.AnticheatLog.Status(context.Background(), uuidString(target.player.UUID)); st != anticheat.StatusSuspicious {
		t.Fatal("a non-op reset the status")
	}
}

// offlineTarget is a player who isn't connected: known only by name and
// UUID (the offline-mode one).
func offlineTarget(name string) string { return protocol.OfflineUUID(name) }

func TestManualBanAtMaxIsLogged(t *testing.T) {
	s := New()
	uuid := offlineTarget("Final")
	setStatusFor(t, s, uuid, "Final", anticheat.StatusMax)
	admin := opConn(t, s, "Admin")

	s.RunCommand(admin, "ban Final 30d repeated cheating")
	rec := latestFor(t, s, uuid)
	if rec.Action != anticheat.ActionBan || rec.By != "Admin" || rec.After != anticheat.StatusMax ||
		rec.Reasons[0].Detail != "repeated cheating" {
		t.Fatalf("manual ban record = %+v", rec)
	}
	if d := time.Until(rec.BanUntil); d < 29*24*time.Hour {
		t.Fatalf("ban until %v", rec.BanUntil)
	}
}

func TestManualBanBelowMaxNotLogged(t *testing.T) {
	s := New()
	uuid := offlineTarget("Grief")
	setStatusFor(t, s, uuid, "Grief", anticheat.StatusSuspicious)
	admin := opConn(t, s, "Admin")
	s.RunCommand(admin, "ban Grief 1d griefing")
	if rec := latestFor(t, s, uuid); rec.By == "Admin" {
		t.Fatalf("an ordinary ban went into the anticheat log: %+v", rec)
	}
}
