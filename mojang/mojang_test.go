package mojang

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestServerHash: the wiki.vg reference digests (signed hex, no padding).
func TestServerHash(t *testing.T) {
	for in, want := range map[string]string{
		"Notch": "4ed1f46bbe04bc756bcb17c0c7ce3e4632f06a48",
		"jeb_":  "-7c9d5b0044c130109a5d7b5fb5c317c02b4e28c1",
		"simon": "88e16a1019277b15d58faf0541e11910eb756f6",
	} {
		if got := ServerHash(in, nil, nil); got != want {
			t.Errorf("ServerHash(%q) = %s, want %s", in, got, want)
		}
	}
}

// withServer points SessionServer at h for one test.
func withServer(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	orig := SessionServer
	SessionServer = srv.URL
	t.Cleanup(func() { SessionServer = orig })
}

// TestVerifyEscapesUsername: a name carrying '&' / '=' used to be pasted
// into the query raw and could override serverId.
func TestVerifyEscapesUsername(t *testing.T) {
	var gotName, gotHash string
	var hashes int
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotName = r.URL.Query().Get("username")
		gotHash = r.URL.Query().Get("serverId")
		hashes = len(r.URL.Query()["serverId"])
		_, _ = w.Write([]byte(`{"id":"069a79f444e94726a5befca90e38aaf5","name":"Notch"}`))
	})
	evil := "x&serverId=forged"
	p, err := VerifyWithMojang(context.Background(), evil, "", []byte("secret"), []byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	if gotName != evil || hashes != 1 || gotHash != ServerHash("", []byte("secret"), []byte("key")) {
		t.Errorf("query: username=%q serverId=%q (×%d)", gotName, gotHash, hashes)
	}
	if p.Name != "Notch" {
		t.Errorf("profile %+v", p)
	}
}

// TestVerifyNotJoined: 204 (not joined) and an empty profile both fail.
func TestVerifyNotJoined(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	if _, err := VerifyWithMojang(context.Background(), "Notch", "", nil, nil); err == nil {
		t.Error("204 must fail")
	}
	withServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	if _, err := VerifyWithMojang(context.Background(), "Notch", "", nil, nil); err == nil {
		t.Error("an empty profile must fail")
	}
}

// TestVerifyHonoursContext: a hung session server doesn't hold the login
// past its deadline.
func TestVerifyHonoursContext(t *testing.T) {
	release := make(chan struct{})
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := VerifyWithMojang(ctx, "Notch", "", nil, nil); err == nil {
		t.Fatal("a hung server must fail")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}
