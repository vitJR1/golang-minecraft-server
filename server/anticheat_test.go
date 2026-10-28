package server

import (
	"bytes"
	"testing"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/protocol"
)

// flagged reports whether check has a non-zero violation level on conn.
func flagged(conn *ClientConnection, check string) bool {
	return conn.ac.Violations.Level(check, time.Now()) > 0
}

// waitFlagged waits for check to be flagged on conn.
func waitFlagged(t *testing.T, conn *ClientConnection, check string) {
	t.Helper()
	waitFor(t, time.Second, func() bool { return flagged(conn, check) }, check+" to be flagged")
}

// tryWrite is write without failing the test: for a client that is
// expected to be kicked mid-stream.
func (tc *testClient) tryWrite(id int32, payload []byte) {
	tc.writeMu.Lock()
	defer tc.writeMu.Unlock()
	_ = protocol.WritePacket(tc.conn, id, payload, tc.threshold)
}

// loginOne logs one player into a fresh server with a draining client.
func loginOne(t *testing.T, name string) (*Server, *testClient, *ClientConnection) {
	t.Helper()
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, name)
	cli.startDiscardDrain()
	return s, cli, findConn(t, s, name)
}

func TestMovementIgnoredUntilTeleportConfirmed(t *testing.T) {
	_, cli, conn := loginOne(t, "Lagger")
	// A server teleport the client hasn't confirmed yet (registered directly,
	// so the draining client can't auto-confirm it).
	id := conn.ac.Teleports.Begin(0.5, 67, 0.5)

	// A move sent before the confirm predates the teleport: dropped. The
	// forged confirm after it is handled strictly later (packets are
	// sequential), so once it's flagged the move has been processed too.
	writeSetPos(t, cli, 2.5, 67, 0.5)
	cli.write(t, SbPlayTeleportConfirm, protocol.WriteVarInt32(9999))
	waitFlagged(t, conn, "badpackets.teleport")
	if x := conn.player.Snapshot().X; x != 0.5 {
		t.Fatalf("move applied while a teleport was pending: x=%v", x)
	}

	// The real confirm opens the gate.
	cli.write(t, SbPlayTeleportConfirm, protocol.WriteVarInt32(id))
	writeSetPos(t, cli, 1.5, 67, 0.5)
	waitFor(t, time.Second, func() bool {
		return conn.player.Snapshot().X == 1.5
	}, "move after the confirm to apply")
}

func TestPongConfirmsTransaction(t *testing.T) {
	_, _, conn := loginOne(t, "Ponger")

	done := make(chan struct{})
	conn.sendTransaction(func() { close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("transaction callback never ran")
	}
	if _, ok := conn.ac.Transactions.LastConfirmedTick(); !ok {
		t.Fatal("no confirmed tick after a pong")
	}
	if conn.ac.Transactions.Outstanding() != 0 {
		t.Fatal("pong left the ping outstanding")
	}
}

func TestForgedPongFlagged(t *testing.T) {
	_, cli, conn := loginOne(t, "Forger")
	cli.write(t, SbPlayPong, protocol.WriteInt(424242))
	waitFlagged(t, conn, "badpackets.transaction")
}

func TestTickTransactionsAndHistory(t *testing.T) {
	s := New()
	s.TickTransactions = true
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Ticker")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Ticker")

	waitFor(t, 2*time.Second, func() bool {
		tick, ok := conn.ac.Transactions.LastConfirmedTick()
		return ok && tick > 0 && len(conn.ac.History.Since(0)) >= 3
	}, "per-tick pings answered and positions recorded")
	if flagged(conn, "badpackets.transaction") {
		t.Fatal("in-order pongs were flagged")
	}
	if s, _ := conn.ac.History.Latest(); s.Y != 67 {
		t.Fatalf("history sample = %+v, want the spawn position", s)
	}
}

func TestHeldSlotOutOfRangeIgnored(t *testing.T) {
	_, cli, conn := loginOne(t, "Slotter")
	cli.write(t, SbPlaySetHeldItem, protocol.WriteShort(4))
	waitFor(t, time.Second, func() bool { return conn.heldSlot.Load() == 4 }, "legit held slot")

	cli.write(t, SbPlaySetHeldItem, protocol.WriteShort(-5))
	waitFlagged(t, conn, "badpackets.helditem")
	if got := conn.heldSlot.Load(); got != 4 {
		t.Fatalf("held slot = %d, want 4 kept", got)
	}
}

func TestPitchOutOfRangeDropped(t *testing.T) {
	_, cli, conn := loginOne(t, "Derp")
	var p bytes.Buffer
	p.Write(protocol.WriteFloat(10))  // yaw
	p.Write(protocol.WriteFloat(180)) // pitch
	p.WriteByte(1)
	cli.write(t, SbPlaySetRot, p.Bytes())
	waitFlagged(t, conn, "badpackets.pitch")
	if s := conn.player.Snapshot(); s.Pitch != 0 || s.Yaw != 0 {
		t.Fatalf("bad rotation applied: yaw=%v pitch=%v", s.Yaw, s.Pitch)
	}
}

func TestForgedKeepAliveFlagged(t *testing.T) {
	_, cli, conn := loginOne(t, "Echo")
	cli.write(t, SbPlayKeepAlive, protocol.WriteLong(123))
	waitFlagged(t, conn, "badpackets.keepalive")
}

func TestPlayerCommandSneakAndForeignEntity(t *testing.T) {
	_, cli, conn := loginOne(t, "Sneaky")
	cmd := func(eid int32, action int32) []byte {
		var b bytes.Buffer
		protocol.WriteVarInt32ToBuffer(&b, eid)
		protocol.WriteVarInt32ToBuffer(&b, action)
		protocol.WriteVarInt32ToBuffer(&b, 0)
		return b.Bytes()
	}
	cli.write(t, SbPlayPlayerCommand, cmd(conn.player.EntityID, 0))
	waitFor(t, time.Second, conn.ac.Sneaking.Load, "sneak input recorded")

	cli.write(t, SbPlayPlayerCommand, cmd(conn.player.EntityID+100, 1))
	waitFlagged(t, conn, "badpackets.command")
	if !conn.ac.Sneaking.Load() {
		t.Fatal("a command for another entity changed our sneak state")
	}
}

func TestPacketFloodKicks(t *testing.T) {
	_, cli, conn := loginOne(t, "Flooder")
	var p bytes.Buffer
	p.Write(protocol.WriteFloat(0))
	p.Write(protocol.WriteFloat(0))
	p.WriteByte(1)
	// Far more than the burst, faster than the refill rate can cover.
	for i := 0; i < 5*int(anticheat.PacketBurst) && !conn.isClosed(); i++ {
		cli.tryWrite(SbPlaySetRot, p.Bytes())
	}
	waitFor(t, time.Second, conn.isClosed, "flooding client to be kicked")
}
