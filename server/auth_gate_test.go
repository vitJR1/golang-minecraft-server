package server

import "testing"

func TestGateAuthAllowsWhenDisabled(t *testing.T) {
	c := &ClientConnection{playerName: "nobody", server: New()}
	if !gateAuth(c, "blocked") {
		t.Error("gateAuth should allow without an auth gate")
	}
}
