package server

import (
	"errors"
	"net"
	"testing"
	"time"

	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/servertest"
)

// TestPrePlayTimeoutClosesIdleConnection: a connection that never reaches
// the play state — here one that handshakes into status and then trickles
// nothing — is closed after prePlayTimeout, however it spaces its reads.
func TestPrePlayTimeoutClosesIdleConnection(t *testing.T) {
	old := prePlayTimeout
	prePlayTimeout = 100 * time.Millisecond
	t.Cleanup(func() { prePlayTimeout = old })

	cli := servertest.Dial(t, New())
	cli.Write(t, packetid.SbHandshake, servertest.Handshake(763, "localhost", 25565, 1))

	_ = cli.Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := protocol.ReadPacket(cli.Conn, protocol.CompressionDisabled)
	if err == nil {
		t.Fatal("the server sent something to an idle status connection")
	}
	if ne := (net.Error)(nil); errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("the idle connection was still open after 2 s")
	}
}
