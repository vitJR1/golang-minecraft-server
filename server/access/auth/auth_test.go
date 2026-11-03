package auth

import (
	"context"
	"errors"

	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"minecraft-server/db"
	"minecraft-server/protocol"
	"minecraft-server/store"
)

// TestBcryptRoundTrip: HashPassword output verifies and rejects a different
// password.
func TestBcryptRoundTrip(t *testing.T) {
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("secret")) != nil {
		t.Error("hash should accept the registered password")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("different")) == nil {
		t.Error("hash should reject a different password")
	}
}

// TestDBCredentials verifies the production path: register/login credentials
// land in (and read back from) the players.password_hash column. Skipped when
// no database is reachable.
func TestDBCredentials(t *testing.T) {
	ctx := context.Background()
	d, err := db.Connect(ctx, db.ConfigFromEnv())
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(d.Close)
	if err := store.Migrate(db.ConfigFromEnv().DSN()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(d.Pool)
	cs := DBCredentials{Players: st.Players}

	name := "DBAuthUser"
	uuid := protocol.OfflineUUID(name)
	if _, err := d.Pool.Exec(ctx, `DELETE FROM players WHERE uuid = $1::uuid`, uuid); err != nil {
		t.Fatal(err)
	}

	// No credential before register.
	if _, ok, err := cs.Hash(ctx, uuid, name); err != nil || ok {
		t.Fatalf("fresh lookup: ok=%v err=%v", ok, err)
	}
	// Register stores the hash...
	hash, _ := HashPassword("pw")
	if err := cs.Set(ctx, uuid, name, hash); err != nil {
		t.Fatal(err)
	}
	// ...login reads it back...
	got, ok, err := cs.Hash(ctx, uuid, name)
	if err != nil || !ok || got != hash {
		t.Fatalf("round trip: got=%q ok=%v err=%v", got, ok, err)
	}
	// ...and it really lives in the players table.
	p, err := st.Players.GetByUUID(ctx, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if h, ok, _ := st.Players.PasswordHash(ctx, p.ID); !ok || h != hash {
		t.Errorf("password_hash not persisted on players row: ok=%v", ok)
	}
}

func TestServiceRegisterAndVerify(t *testing.T) {
	ctx := context.Background()
	svc := &Service{Creds: NewMemCredentials()}
	if err := svc.Register(ctx, "Eve", "abc"); !errors.Is(err, ErrTooShort) {
		t.Fatalf("short password: %v", err)
	}
	if err := svc.Verify(ctx, "eve", "secret"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("verify before register: %v", err)
	}
	if err := svc.Register(ctx, "Eve", "secret"); err != nil {
		t.Fatal(err)
	}
	if !svc.Registered("EVE") {
		t.Error("Registered should be case-insensitive")
	}
	if err := svc.Register(ctx, "eve", "other1"); !errors.Is(err, ErrRegistered) {
		t.Fatalf("second register: %v", err)
	}
	if err := svc.Verify(ctx, "eve", "wrong!"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := svc.Verify(ctx, "eve", "secret"); err != nil {
		t.Fatalf("right password: %v", err)
	}
}

// guardAt returns a guard on a temp file with a clock the test moves.
func guardAt(t *testing.T) (*IPGuard, *time.Time) {
	t.Helper()
	g := NewIPGuard(filepath.Join(t.TempDir(), "auth.json.ipbans.json"))
	now := time.Unix(1_000_000, 0)
	g.now = func() time.Time { return now }
	return g, &now
}

func TestIPGuardBansAtMaxAttemptsAndExpires(t *testing.T) {
	g, now := guardAt(t)
	if g.RecordFail("1.2.3.4", "victim", 2, time.Hour) {
		t.Fatal("first fail must not ban")
	}
	if _, ok := g.Allowed("1.2.3.4"); !ok {
		t.Fatal("one fail is still allowed")
	}
	if !g.RecordFail("1.2.3.4", "Victim", 2, time.Hour) {
		t.Fatal("second fail must ban")
	}
	if until, ok := g.Allowed("1.2.3.4"); ok || !until.Equal(now.Add(time.Hour)) {
		t.Fatalf("banned: until=%v ok=%v", until, ok)
	}
	if _, ok := g.Allowed("5.6.7.8"); !ok {
		t.Error("other IPs stay allowed")
	}
	*now = now.Add(time.Hour + time.Second)
	if _, ok := g.Allowed("1.2.3.4"); !ok {
		t.Fatal("ban should have expired")
	}
	if g.Fails("1.2.3.4", "victim") != 0 {
		t.Error("an expired ban resets the fail count")
	}
}

func TestIPGuardClearFailsAndUnknownIP(t *testing.T) {
	g, _ := guardAt(t)
	g.RecordFail("1.2.3.4", "me", 3, time.Hour)
	g.ClearFails("1.2.3.4", "me")
	if g.Fails("1.2.3.4", "me") != 0 {
		t.Error("ClearFails should reset the counter")
	}
	if g.RecordFail("", "me", 1, time.Hour) {
		t.Error("an unknown IP is never banned")
	}
	if _, ok := g.Allowed(""); !ok {
		t.Error("an unknown IP is always allowed")
	}
}

func TestIPGuardPersistsBans(t *testing.T) {
	g, now := guardAt(t)
	g.Ban("1.1.1.1", time.Hour)
	g.Ban("2.2.2.2", time.Minute)

	back := NewIPGuard(g.Path())
	later := now.Add(2 * time.Minute)
	back.now = func() time.Time { return later }
	if err := back.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := back.Allowed("1.1.1.1"); ok {
		t.Error("the hour ban should survive a reload")
	}
	back.mu.Lock()
	_, stale := back.bans["2.2.2.2"]
	back.mu.Unlock()
	if stale {
		t.Error("a ban expired while offline should be dropped on load")
	}
	if err := NewIPGuard(filepath.Join(t.TempDir(), "missing.json")).Load(); err != nil {
		t.Errorf("a missing file is fine: %v", err)
	}
}

func TestIPGuardResolveAndUnban(t *testing.T) {
	g, _ := guardAt(t)
	g.Remember("Mallory", "6.6.6.6")
	if ip := g.Resolve("mallory"); ip != "6.6.6.6" {
		t.Fatalf("Resolve by name: %q", ip)
	}
	if ip := g.Resolve("9.9.9.9"); ip != "9.9.9.9" {
		t.Fatalf("Resolve falls back to the literal: %q", ip)
	}
	if g.Unban("6.6.6.6") {
		t.Error("Unban of a clean IP reports false")
	}
	g.Ban("6.6.6.6", time.Hour)
	if !g.Unban("6.6.6.6") {
		t.Error("Unban of a banned IP reports true")
	}
	if _, ok := g.Allowed("6.6.6.6"); !ok {
		t.Error("unbanned IP is allowed")
	}
}
