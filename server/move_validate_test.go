package server

import (
	"bytes"
	"math"
	"minecraft-server/protocol"
	"testing"
	"time"
)

// writeSetPos builds and sends a SbPlaySetPos payload (3 doubles + onGround).
func writeSetPos(t *testing.T, cli *testClient, x, y, z float64) {
	t.Helper()
	var p bytes.Buffer
	p.Write(protocol.WriteDouble(x))
	p.Write(protocol.WriteDouble(y))
	p.Write(protocol.WriteDouble(z))
	p.WriteByte(1) // onGround
	cli.write(t, SbPlaySetPos, p.Bytes())
}

func TestMoveValidAccepted(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Walker")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Walker")

	// Default spawn is (0.5, 67, 0.5); a couple of blocks away is legit.
	writeSetPos(t, cli, 2.5, 67, 3.5)
	waitFor(t, time.Second, func() bool {
		snap := conn.player.Snapshot()
		return snap.X == 2.5 && snap.Z == 3.5
	}, "legit move to apply")
}

func TestMoveTooFastRejected(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Teleporter")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Teleporter")

	// 200 blocks in one packet — must be rejected (position unchanged).
	writeSetPos(t, cli, 200.5, 67, 0.5)
	// A follow-up move that is only in range from the ORIGINAL spawn proves
	// the hack packet was processed AND ignored (packets are sequential).
	writeSetPos(t, cli, 3.5, 67, 0.5)
	waitFor(t, time.Second, func() bool {
		return conn.player.Snapshot().X == 3.5
	}, "follow-up move from the original position to apply")
}

func TestMoveOutOfBoundsRejected(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Voider")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Voider")

	// In-range delta can't reach out-of-bounds coords from spawn, so force
	// the server-side position near the border first.
	conn.player.MoveTo(maxWorldXZ-1, 67, 0.5, true)
	writeSetPos(t, cli, maxWorldXZ+5, 67, 0.5)
	writeSetPos(t, cli, maxWorldXZ-4, 67, 0.5) // legit follow-up
	waitFor(t, time.Second, func() bool {
		return conn.player.Snapshot().X == maxWorldXZ-4
	}, "follow-up move to apply after out-of-bounds rejection")
}

func TestMoveNaNKicks(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Fuzzer")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Fuzzer")

	writeSetPos(t, cli, math.NaN(), 67, 0.5)
	waitFor(t, time.Second, func() bool { return conn.isClosed() },
		"NaN movement to kick the connection")
}
