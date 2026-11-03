package server

import (
	"net"
	"testing"
)

func TestGateAuthAllowsWhenDisabled(t *testing.T) {
	c := &ClientConnection{playerName: "nobody", server: New()}
	if !gateAuth(c, "blocked") {
		t.Error("gateAuth should allow without an auth gate")
	}
}

// TestClientIPKeysIPv6By64: the per-IP limits, auth IP bans and /banip all
// key by ClientIP, so two addresses of one IPv6 /64 must be one client.
func TestClientIPKeysIPv6By64(t *testing.T) {
	a := ClientIP(&net.TCPAddr{IP: net.ParseIP("2001:db8:5:6::1"), Port: 1})
	b := ClientIP(&net.TCPAddr{IP: net.ParseIP("2001:db8:5:6:abcd::2"), Port: 2})
	if a != b || a != "2001:db8:5:6::/64" {
		t.Errorf("same /64 keyed %q and %q", a, b)
	}
	if v4 := ClientIP(&net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 3}); v4 != "10.1.2.3" {
		t.Errorf("IPv4 keyed %q", v4)
	}
}
