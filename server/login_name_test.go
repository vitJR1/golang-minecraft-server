package server

import (
	"strings"
	"testing"

	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/servertest"
)

// TestOversizedLoginNameRejected: a 40 KB Login Start name used to reach
// protocol.WriteString in the play packets and panic the whole process; now
// it gets a Login Disconnect and the server keeps serving.
func TestOversizedLoginNameRejected(t *testing.T) {
	s := New()
	name := strings.Repeat("a", 40000)
	raw := append(protocol.WriteVarInt32(int32(len(name))), name...) // WriteString panics on it
	cli := servertest.Dial(t, s)
	cli.Write(t, packetid.SbHandshake, servertest.Handshake(763, "localhost", 25565, 2))
	cli.Write(t, packetid.SbLoginStart, raw)
	if id, _ := cli.Read(t); id != packetid.CbLoginDisconnect {
		t.Fatalf("got packet 0x%02X, want Login Disconnect", id)
	}

	ok := servertest.Dial(t, s)
	ok.Login(t, "Normal")
}
