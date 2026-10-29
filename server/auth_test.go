package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"minecraft-server/cfg"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/auth"
)

// withAuth installs a fresh auth plugin against the given Server, then
// restores authStore=nil on cleanup so later tests aren't gated.
func withAuth(t *testing.T, s *Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	EnableAuth(s, path)
	t.Cleanup(func() { authStore = nil })
	return path
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
	withAuth(t, New()) // no DB → in-memory credential store
	if err := authStore.svc.Creds.Set(context.Background(), protocol.OfflineUUID("Alice"), "Alice", "h"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"alice", "ALICE", "Alice"} {
		if !authStore.svc.Registered(n) {
			t.Errorf("has(%q) should be true", n)
		}
	}
	if authStore.svc.Registered("bob") {
		t.Error("has(bob) should be false")
	}
}

// TestCredStoreRoundTrip: a hash written via Set reads back via Hash through
// the plugin's credential store (the in-memory backend used without a DB).
func TestCredStoreRoundTrip(t *testing.T) {
	withAuth(t, New())
	hash, _ := auth.HashPassword("diskpass")
	ctx := context.Background()
	uuid := protocol.OfflineUUID("dan")
	if err := authStore.svc.Creds.Set(ctx, uuid, "dan", hash); err != nil {
		t.Fatal(err)
	}
	got, ok, err := authStore.svc.Creds.Hash(ctx, uuid, "dan")
	if err != nil || !ok || got != hash {
		t.Fatalf("round trip failed: got=%q ok=%v err=%v", got, ok, err)
	}
}

func TestGateAuthAllowsWhenDisabled(t *testing.T) {
	c := &ClientConnection{playerName: "nobody"}
	c.authed.Store(true) // default in real HandleConn
	if !gateAuth(c, "blocked") {
		t.Error("gateAuth should allow when auth plugin is disabled")
	}
}

// --- Integration: instance routing -----------------------------------------

// TestUnauthedLandsInAuthInstance: with auth enabled, freshly-logged-in
// players go into srv.Auth, not srv.Hub.
func TestUnauthedLandsInAuthInstance(t *testing.T) {
	s := New()
	withAuth(t, s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "newcomer")
	cli.startDiscardDrain()

	conn := findConn(t, s, "newcomer")
	if conn.instance != s.Auth {
		t.Errorf("conn.instance: got %v, want Auth", conn.instance.ID)
	}
	if conn.authed.Load() {
		t.Error("authed should be false in auth instance")
	}
}

// TestRegisterMovesPlayerToHub: successful /register flips authed AND
// migrates the connection into Hub.
func TestRegisterMovesPlayerToHub(t *testing.T) {
	s := New()
	withAuth(t, s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "alice")
	cli.startDiscardDrain()

	conn := findConn(t, s, "alice")
	cli.write(t, packetid.SbPlayChatCommand, protocol.WriteString("register hunter2 hunter2"))

	waitFor(t, 2*time.Second, func() bool { return conn.authed.Load() },
		"alice to be authed")
	waitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("alice")
		return ok && inst == s.Hub
	}, "alice to land in hub")

	// Credential should be persisted in the store.
	if !authStore.svc.Registered("alice") {
		t.Error("alice missing from credential store after register")
	}
}

func TestRegisterMismatchedPasswordsRejected(t *testing.T) {
	s := New()
	withAuth(t, s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "bob")
	cli.startDiscardDrain()
	conn := findConn(t, s, "bob")

	cli.write(t, packetid.SbPlayChatCommand, protocol.WriteString("register foo bar"))
	time.Sleep(100 * time.Millisecond)
	if conn.authed.Load() {
		t.Error("mismatched passwords must not authenticate")
	}
	if conn.instance != s.Auth {
		t.Error("failed register should leave player in auth instance")
	}
}

// TestLoginAfterPriorRegister: register-then-disconnect, reconnect,
// /login → moves to hub.
func TestLoginAfterPriorRegister(t *testing.T) {
	s := New()
	withAuth(t, s)

	c1 := pipeClientOn(t, s)
	completeOfflineLogin(t, c1, "carol")
	c1.startDiscardDrain()
	c1.write(t, packetid.SbPlayChatCommand, protocol.WriteString("register mypass mypass"))
	waitFor(t, 2*time.Second, func() bool { return authStore.svc.Registered("carol") },
		"carol registered")

	_ = c1.conn.Close()
	waitFor(t, 2*time.Second, func() bool {
		_, _, ok := s.FindPlayer("carol")
		return !ok
	}, "carol to disconnect")

	c2 := pipeClientOn(t, s)
	completeOfflineLogin(t, c2, "carol")
	c2.startDiscardDrain()
	conn2 := findConn(t, s, "carol")

	c2.write(t, packetid.SbPlayChatCommand, protocol.WriteString("login mypass"))
	waitFor(t, 2*time.Second, func() bool { return conn2.authed.Load() },
		"carol re-login")
	waitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("carol")
		return ok && inst == s.Hub
	}, "carol in hub")
}

// TestGatedCommandRejected: /play before auth is blocked.
func TestGatedCommandRejected(t *testing.T) {
	s := New()
	withAuth(t, s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "wally")
	cli.startDiscardDrain()
	conn := findConn(t, s, "wally")

	cli.write(t, packetid.SbPlayChatCommand, protocol.WriteString("play ffa"))
	time.Sleep(100 * time.Millisecond)
	if conn.authed.Load() {
		t.Error("wally should still be unauthed")
	}
	if _, ok := s.Matchmaker.PlayerQueue(conn); ok {
		t.Error("/play should NOT have queued wally — gate must block")
	}
}

// --- Timeout enforcement ----------------------------------------------------

// TestAuthTimeoutKicks: an idle player with no /login or /register is
// kicked after cfg.AuthTimeout.
func TestAuthTimeoutKicks(t *testing.T) {
	withAuthTiming(t, 100*time.Millisecond)
	s := New()
	withAuth(t, s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "slow")
	cli.startDiscardDrain()

	waitFor(t, 2*time.Second, func() bool {
		_, _, ok := s.FindPlayer("slow")
		return !ok
	}, "slow to be kicked after timeout")
}

// TestAuthTimeoutCancelledOnSuccess: registering before the timeout
// fires keeps the player alive. The timeout has to clear bcrypt's
// ~100ms hash cost plus a generous buffer; 1s gives both the race
// detector and a slow CI enough headroom.
func TestAuthTimeoutCancelledOnSuccess(t *testing.T) {
	withAuthTiming(t, 1*time.Second)
	s := New()
	withAuth(t, s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "fast")
	cli.startDiscardDrain()
	conn := findConn(t, s, "fast")

	cli.write(t, packetid.SbPlayChatCommand, protocol.WriteString("register racepass racepass"))
	waitFor(t, 2*time.Second, func() bool { return conn.authed.Load() },
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
	s := New()
	withAuth(t, s)

	// Seed a registered account so /login attempts can be made.
	hash, err := auth.HashPassword("correctpass")
	if err != nil {
		t.Fatal(err)
	}
	if err := authStore.svc.Creds.Set(context.Background(), protocol.OfflineUUID("target"), "target", hash); err != nil {
		t.Fatal(err)
	}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "target")
	cli.startDiscardDrain()

	// 1st wrong password.
	cli.write(t, packetid.SbPlayChatCommand, protocol.WriteString("login wrong1"))
	time.Sleep(100 * time.Millisecond)
	// 2nd wrong password → trips the ban.
	cli.write(t, packetid.SbPlayChatCommand, protocol.WriteString("login wrong2"))
	waitFor(t, 2*time.Second, func() bool {
		_, _, ok := s.FindPlayer("target")
		return !ok
	}, "target kicked after 2 fails")

	// IP should now be in ipBans.
	if _, allowed := authStore.CheckIPAllowed(cli.conn.RemoteAddr()); allowed {
		t.Error("IP should be banned after max attempts")
	}
}

// TestSuccessfulLoginClearsFailCounter: a correct password between
// failed attempts resets the counter so the user isn't accidentally
// banned later.
func TestSuccessfulLoginClearsFailCounter(t *testing.T) {
	withAuthAttempts(t, 2, 1*time.Hour)
	s := New()
	withAuth(t, s)

	hash, err := auth.HashPassword("correct")
	if err != nil {
		t.Fatal(err)
	}
	if err := authStore.svc.Creds.Set(context.Background(), protocol.OfflineUUID("jane"), "jane", hash); err != nil {
		t.Fatal(err)
	}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "jane")
	cli.startDiscardDrain()
	conn := findConn(t, s, "jane")

	cli.write(t, packetid.SbPlayChatCommand, protocol.WriteString("login wrong"))
	time.Sleep(100 * time.Millisecond)
	cli.write(t, packetid.SbPlayChatCommand, protocol.WriteString("login correct"))
	waitFor(t, 2*time.Second, func() bool { return conn.authed.Load() },
		"jane to log in")

	// Fail counter should be cleared.
	count := authStore.svc.IPs.Fails(clientIP(cli.conn.RemoteAddr()))
	if count != 0 {
		t.Errorf("fail counter not cleared after success: got %d", count)
	}
}
