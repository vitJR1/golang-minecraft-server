package server

import (
	"encoding/base64"
	"encoding/json"
	"minecraft-server/cfg"
	"minecraft-server/protocol"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// --- Unit tests for the credential store ------------------------------------

// TestBcryptRoundTrip: bcryptHash output verifies via verifyCred and
// rejects a different password.
func TestBcryptRoundTrip(t *testing.T) {
	hash, err := bcryptHash("secret")
	if err != nil {
		t.Fatal(err)
	}
	cred := &authCred{Hash: hash}
	if !verifyCred(cred, "secret") {
		t.Error("verifyCred should accept the registered password")
	}
	if verifyCred(cred, "different") {
		t.Error("verifyCred should reject a different password")
	}
}

// TestLegacySHAStillVerifies: a credential written in the old SHA-256
// format (Salt + base64 hash) still authenticates via verifyCred.
func TestLegacySHAStillVerifies(t *testing.T) {
	salt := []byte("0123456789abcdef")
	legacy := &authCred{
		Salt: base64.StdEncoding.EncodeToString(salt),
		Hash: hashLegacySHA(salt, "oldpass"),
	}
	if !verifyCred(legacy, "oldpass") {
		t.Error("legacy SHA-256 entry should still verify with correct password")
	}
	if verifyCred(legacy, "wrong") {
		t.Error("legacy entry should reject wrong password")
	}
}

func TestPluginHasIsCaseInsensitive(t *testing.T) {
	withAuth(t, New())
	authStore.creds["alice"] = &authCred{Salt: "s", Hash: "h"}
	for _, n := range []string{"alice", "ALICE", "Alice"} {
		if !authStore.has(n) {
			t.Errorf("has(%q) should be true", n)
		}
	}
	if authStore.has("bob") {
		t.Error("has(bob) should be false")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := New()
	path := withAuth(t, s)

	hash, err := bcryptHash("diskpass")
	if err != nil {
		t.Fatal(err)
	}
	authStore.creds["dan"] = &authCred{
		Hash:         hash,
		RegisteredAt: time.Now(),
	}
	if err := authStore.save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["dan"]; !ok {
		t.Errorf("dan missing from saved file: %s", data)
	}

	authStore = nil
	EnableAuth(s, path)
	if !authStore.has("dan") {
		t.Fatal("dan should reload from disk")
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
	path := withAuth(t, s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "alice")
	cli.startDiscardDrain()

	conn := findConn(t, s, "alice")
	cli.write(t, SbPlayChatCommand, protocol.WriteString("register hunter2 hunter2"))

	waitFor(t, 2*time.Second, func() bool { return conn.authed.Load() },
		"alice to be authed")
	waitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("alice")
		return ok && inst == s.Hub
	}, "alice to land in hub")

	// File should have the entry persisted.
	data, _ := os.ReadFile(path)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if _, ok := m["alice"]; !ok {
		t.Errorf("alice missing from %s: %s", path, data)
	}
}

func TestRegisterMismatchedPasswordsRejected(t *testing.T) {
	s := New()
	withAuth(t, s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "bob")
	cli.startDiscardDrain()
	conn := findConn(t, s, "bob")

	cli.write(t, SbPlayChatCommand, protocol.WriteString("register foo bar"))
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
	c1.write(t, SbPlayChatCommand, protocol.WriteString("register mypass mypass"))
	waitFor(t, 2*time.Second, func() bool { return authStore.has("carol") },
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

	c2.write(t, SbPlayChatCommand, protocol.WriteString("login mypass"))
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

	cli.write(t, SbPlayChatCommand, protocol.WriteString("play ffa"))
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

	cli.write(t, SbPlayChatCommand, protocol.WriteString("register racepass racepass"))
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
	hash, err := bcryptHash("correctpass")
	if err != nil {
		t.Fatal(err)
	}
	authStore.creds["target"] = &authCred{Hash: hash}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "target")
	cli.startDiscardDrain()

	// 1st wrong password.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("login wrong1"))
	time.Sleep(100 * time.Millisecond)
	// 2nd wrong password → trips the ban.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("login wrong2"))
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

	hash, err := bcryptHash("correct")
	if err != nil {
		t.Fatal(err)
	}
	authStore.creds["jane"] = &authCred{Hash: hash}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "jane")
	cli.startDiscardDrain()
	conn := findConn(t, s, "jane")

	cli.write(t, SbPlayChatCommand, protocol.WriteString("login wrong"))
	time.Sleep(100 * time.Millisecond)
	cli.write(t, SbPlayChatCommand, protocol.WriteString("login correct"))
	waitFor(t, 2*time.Second, func() bool { return conn.authed.Load() },
		"jane to log in")

	// Fail counter should be cleared.
	authStore.ipMu.Lock()
	count := authStore.ipFails[clientIP(cli.conn.RemoteAddr())]
	authStore.ipMu.Unlock()
	if count != 0 {
		t.Errorf("fail counter not cleared after success: got %d", count)
	}
}
