package metrics

import (
	"fmt"
	"net/http"
	"testing"
)

// TestNonLoopbackNeedsToken: an unauthenticated pprof on a public address
// is a CPU-DoS and memory-dump handle, so Start refuses it.
func TestNonLoopbackNeedsToken(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "[::]:0"} {
		if lis, err := Start(fixed{}, addr, ""); err == nil {
			_ = lis.Close()
			t.Errorf("%s without a token should be refused", addr)
		}
	}
	for addr, want := range map[string]bool{
		"127.0.0.1:6060": true, "[::1]:6060": true, "localhost:6060": true,
		"0.0.0.0:6060": false, ":6060": false, "10.0.0.5:6060": false, "garbage": false,
	} {
		if got := loopback(addr); got != want {
			t.Errorf("loopback(%q) = %v", addr, got)
		}
	}
}

// TestTokenRequired: with a token, requests without the right bearer get
// 401 on every endpoint.
func TestTokenRequired(t *testing.T) {
	lis, err := Start(fixed{Players: 1}, "127.0.0.1:0", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	for _, tc := range []struct {
		path, auth string
		want       int
	}{
		{"/stats", "", http.StatusUnauthorized},
		{"/debug/pprof/", "", http.StatusUnauthorized},
		{"/stats", "Bearer wrong", http.StatusUnauthorized},
		{"/stats", "s3cret", http.StatusUnauthorized},
		{"/stats", "Bearer s3cret", http.StatusOK},
		{"/debug/pprof/", "Bearer s3cret", http.StatusOK},
	} {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s%s", lis.Addr(), tc.path), nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s with %q: %d, want %d", tc.path, tc.auth, resp.StatusCode, tc.want)
		}
	}
}
