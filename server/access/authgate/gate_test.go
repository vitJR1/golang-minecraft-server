package authgate

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"minecraft-server/cfg"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server"
	"minecraft-server/server/access/auth"
	"minecraft-server/server/net/servertest"
)

// withAuth installs a fresh auth gate on s.
func withAuth(t *testing.T, s *server.Server) *Gate {
	t.Helper()
	return Enable(s, filepath.Join(t.TempDir(), "auth.json"))
}

// login logs name into s over a draining pipe client and returns its
// connection.
func login(t *testing.T, s *server.Server, name string) (*servertest.Client, *server.ClientConnection) {
	t.Helper()
	cli := servertest.Dial(t, s)
	cli.Login(t, name)
	cli.StartDiscardDrain()
	return cli, findConn(t, s, name)
}

// findConn waits for name's connection to register.
func findConn(t *testing.T, s *server.Server, name string) *server.ClientConnection {
	t.Helper()
	var c *server.ClientConnection
	servertest.WaitFor(t, time.Second, func() bool {
		conn, _, ok := s.FindPlayer(name)
		c = conn
		return ok
	}, name+" to register")
	return c
}

// gone waits for name to leave the server.
func gone(t *testing.T, s *server.Server, name, msg string) {
	t.Helper()
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, _, ok := s.FindPlayer(name)
		return !ok
	}, msg)
}

// withAuthTiming shrinks AuthTimeout for one test (30s in prod, ~ms here).
func withAuthTiming(t *testing.T, timeout time.Duration) {
	t.Helper()
	orig := cfg.AuthTimeout()
	cfg.SetAuthTimeout(timeout)
	t.Cleanup(func() { cfg.SetAuthTimeout(orig) })
}

// withAuthAttempts shrinks AuthMaxAttempts + ban duration for tests.
func withAuthAttempts(t *testing.T, max int, banDur time.Duration) {
	t.Helper()
	origM, origD := cfg.AuthMaxAttempts(), cfg.AuthBanDuration()
	cfg.SetAuthMaxAttempts(max)
	cfg.SetAuthBanDuration(banDur)
	t.Cleanup(func() {
		cfg.SetAuthMaxAttempts(origM)
		cfg.SetAuthBanDuration(origD)
	})
}

// --- Plugin wiring ----------------------------------------------------------

func TestPluginHasIsCaseInsensitive(t *testing.T) {
	g := withAuth(t, server.New()) // no DB → in-memory credential store
	if err := g.svc.Creds.Set(context.Background(), protocol.OfflineUUID("Alice"), "Alice", "h"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"alice", "ALICE", "Alice"} {
		if !g.svc.Registered(n) {
			t.Errorf("has(%q) should be true", n)
		}
	}
	if g.svc.Registered("bob") {
		t.Error("has(bob) should be false")
	}
}

// TestCredStoreRoundTrip: a hash written via Set reads back via Hash through
// the plugin's credential store (the in-memory backend used without a DB).
func TestCredStoreRoundTrip(t *testing.T) {
	g := withAuth(t, server.New())
	hash, _ := auth.HashPassword("diskpass")
	ctx := context.Background()
	uuid := protocol.OfflineUUID("dan")
	if err := g.svc.Creds.Set(ctx, uuid, "dan", hash); err != nil {
		t.Fatal(err)
	}
	got, ok, err := g.svc.Creds.Hash(ctx, uuid, "dan")
	if err != nil || !ok || got != hash {
		t.Fatalf("round trip failed: got=%q ok=%v err=%v", got, ok, err)
	}
}

// --- Integration: instance routing -----------------------------------------

// TestUnauthedLandsInAuthInstance: with auth enabled, freshly-logged-in
// players go into srv.Auth, not srv.Hub.
func TestUnauthedLandsInAuthInstance(t *testing.T) {
	s := server.New()
	withAuth(t, s)
	_, conn := login(t, s, "newcomer")
	if conn.Instance() != s.Auth {
		t.Errorf("conn.instance: got %v, want Auth", conn.Instance().ID)
	}
	if conn.Authed() {
		t.Error("authed should be false in auth instance")
	}
}

// TestRegisterMovesPlayerToHub: successful /register flips authed AND
// migrates the connection into Hub.
func TestRegisterMovesPlayerToHub(t *testing.T) {
	s := server.New()
	g := withAuth(t, s)
	cli, conn := login(t, s, "alice")
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("register hunter2 hunter2"))

	servertest.WaitFor(t, 2*time.Second, func() bool { return conn.Authed() },
		"alice to be authed")
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("alice")
		return ok && inst == s.Hub
	}, "alice to land in hub")

	// Credential should be persisted in the store.
	if !g.svc.Registered("alice") {
		t.Error("alice missing from credential store after register")
	}
}

func TestRegisterMismatchedPasswordsRejected(t *testing.T) {
	s := server.New()
	withAuth(t, s)
	cli, conn := login(t, s, "bob")

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("register foo bar"))
	time.Sleep(100 * time.Millisecond)
	if conn.Authed() {
		t.Error("mismatched passwords must not authenticate")
	}
	if conn.Instance() != s.Auth {
		t.Error("failed register should leave player in auth instance")
	}
}

// TestLoginAfterPriorRegister: register-then-disconnect, reconnect,
// /login → moves to hub.
func TestLoginAfterPriorRegister(t *testing.T) {
	s := server.New()
	g := withAuth(t, s)

	c1, _ := login(t, s, "carol")
	c1.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("register mypass mypass"))
	servertest.WaitFor(t, 2*time.Second, func() bool { return g.svc.Registered("carol") },
		"carol registered")

	_ = c1.Conn.Close()
	gone(t, s, "carol", "carol to disconnect")

	c2, conn2 := login(t, s, "carol")

	c2.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("login mypass"))
	servertest.WaitFor(t, 2*time.Second, func() bool { return conn2.Authed() },
		"carol re-login")
	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("carol")
		return ok && inst == s.Hub
	}, "carol in hub")
}

// TestGatedCommandRejected: a command before auth is blocked.
func TestGatedCommandRejected(t *testing.T) {
	s := server.New()
	withAuth(t, s)
	cli, conn := login(t, s, "wally")

	var ran atomic.Bool
	server.RegisterCommand(&server.Command{Name: "gatetest", Run: func(*server.ClientConnection, []string) { ran.Store(true) }})
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("gatetest"))
	time.Sleep(100 * time.Millisecond)
	if conn.Authed() {
		t.Error("wally should still be unauthed")
	}
	if ran.Load() {
		t.Error("the command ran — the gate must block it")
	}
}

// --- Timeout enforcement ----------------------------------------------------

// TestAuthTimeoutKicks: an idle player with no /login or /register is
// kicked after cfg.AuthTimeout.
func TestAuthTimeoutKicks(t *testing.T) {
	withAuthTiming(t, 100*time.Millisecond)
	s := server.New()
	withAuth(t, s)
	login(t, s, "slow")

	gone(t, s, "slow", "slow to be kicked after timeout")
}

// TestAuthTimeoutCancelledOnSuccess: registering before the timeout
// fires keeps the player alive. The timeout has to clear bcrypt's
// ~100ms hash cost plus a generous buffer; 1s gives both the race
// detector and a slow CI enough headroom.
func TestAuthTimeoutCancelledOnSuccess(t *testing.T) {
	withAuthTiming(t, 1*time.Second)
	s := server.New()
	withAuth(t, s)
	cli, conn := login(t, s, "fast")

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("register racepass racepass"))
	servertest.WaitFor(t, 2*time.Second, func() bool { return conn.Authed() },
		"fast to register before timeout")

	// Wait past the timeout window — fast must still be online.
	time.Sleep(1500 * time.Millisecond)
	if _, _, ok := s.FindPlayer("fast"); !ok {
		t.Error("fast should NOT be kicked after successful register")
	}
}

// --- IP ban enforcement -----------------------------------------------------

// TestIPBannedAfterMaxAttempts: 2 wrong passwords (with limit=2) trips
// the IP ban and disconnects the player.
func TestIPBannedAfterMaxAttempts(t *testing.T) {
	withAuthAttempts(t, 2, 1*time.Hour)
	s := server.New()
	g := withAuth(t, s)

	// Seed a registered account so /login attempts can be made.
	hash, err := auth.HashPassword("correctpass")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.svc.Creds.Set(context.Background(), protocol.OfflineUUID("target"), "target", hash); err != nil {
		t.Fatal(err)
	}

	cli, _ := login(t, s, "target")

	// 1st wrong password.
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("login wrong1"))
	time.Sleep(100 * time.Millisecond)
	// 2nd wrong password → trips the ban.
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("login wrong2"))
	gone(t, s, "target", "target kicked after 2 fails")

	// IP should now be in ipBans.
	if _, banned := g.IPBanned(server.ClientIP(cli.Conn.RemoteAddr())); !banned {
		t.Error("IP should be banned after max attempts")
	}
}

// TestSuccessfulLoginClearsFailCounter: a correct password between
// failed attempts resets the counter so the user isn't accidentally
// banned later.
func TestSuccessfulLoginClearsFailCounter(t *testing.T) {
	withAuthAttempts(t, 2, 1*time.Hour)
	s := server.New()
	g := withAuth(t, s)

	hash, err := auth.HashPassword("correct")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.svc.Creds.Set(context.Background(), protocol.OfflineUUID("jane"), "jane", hash); err != nil {
		t.Fatal(err)
	}

	cli, conn := login(t, s, "jane")

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("login wrong"))
	time.Sleep(100 * time.Millisecond)
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("login correct"))
	servertest.WaitFor(t, 2*time.Second, func() bool { return conn.Authed() },
		"jane to log in")

	// Fail counter should be cleared.
	count := g.svc.IPs.Fails(server.ClientIP(cli.Conn.RemoteAddr()), "jane")
	if count != 0 {
		t.Errorf("fail counter not cleared after success: got %d", count)
	}
}
