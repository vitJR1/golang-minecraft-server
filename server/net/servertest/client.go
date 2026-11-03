// Package servertest is a protocol-level test client for the server: a
// vanilla-like client on one end of a net.Pipe whose other end goes to
// HandleConn. It doesn't import the server — anything with HandleConn
// (server.Server) plugs in — so the server's own tests and the feature
// packages built over it (server/features/commands, …) share it.
package servertest

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"

	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
)

// Handler accepts a client socket (server.Server.HandleConn).
type Handler interface {
	HandleConn(conn net.Conn)
}

// Client is the client end of one connection.
type Client struct {
	Conn      net.Conn
	Threshold int // compression threshold, set by Login

	// writeMu serialises frames: the drain goroutines answer teleports and
	// Pings (AutoReply) while the test itself writes.
	writeMu sync.Mutex
}

// Dial wires h.HandleConn to one side of a net.Pipe and returns a Client for
// the other side, compression disabled. Cleanup closes the pipe and waits
// (up to a second) for HandleConn to return.
func Dial(t *testing.T, h Handler) *Client {
	t.Helper()
	cli, srv := net.Pipe()

	done := make(chan struct{})
	go func() {
		h.HandleConn(srv)
		close(done)
	}()
	// t.Cleanup runs LIFO. Close the client first, then wait for the server
	// goroutine — closing cli is what unblocks the server's readLoop.
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Log("server did not exit within 1s after pipe close")
		}
	})
	t.Cleanup(func() { _ = cli.Close() })
	return &Client{Conn: cli, Threshold: protocol.CompressionDisabled}
}

// Write sends one packet.
func (tc *Client) Write(t *testing.T, id int32, payload []byte) {
	t.Helper()
	tc.writeMu.Lock()
	err := protocol.WritePacket(tc.Conn, id, payload, tc.Threshold)
	tc.writeMu.Unlock()
	if err != nil {
		t.Fatalf("write packet 0x%02X: %v", id, err)
	}
}

// TryWrite is Write without failing the test: for a client that is
// expected to be kicked mid-stream.
func (tc *Client) TryWrite(id int32, payload []byte) {
	tc.writeMu.Lock()
	defer tc.writeMu.Unlock()
	_ = protocol.WritePacket(tc.Conn, id, payload, tc.Threshold)
}

// Command sends a Chat Command (text without the leading slash).
func (tc *Client) Command(t *testing.T, text string) {
	t.Helper()
	tc.Write(t, packetid.SbPlayChatCommand, protocol.WriteString(text))
}

// AutoReply answers what a vanilla client answers on its own: Confirm
// Teleportation for every Synchronize Player Position (the server ignores
// movement until then) and Pong for every Ping. body is the packet payload
// after the id; it is read from a copy, so the caller's buffer is untouched.
// Write errors are ignored — the pipe closing ends the test anyway.
func (tc *Client) AutoReply(id int, body []byte) {
	var reply []byte
	var replyID int32
	switch id {
	case packetid.CbPlaySyncPos:
		// x, y, z (3×Double) + yaw, pitch (2×Float) + flags (Byte) = 33 bytes.
		if len(body) < 33 {
			return
		}
		tp, _, err := protocol.ReadVarIntFromBytes(body[33:])
		if err != nil {
			return
		}
		replyID, reply = packetid.SbPlayTeleportConfirm, protocol.WriteVarInt32(int32(tp))
	case packetid.CbPlayPing:
		if len(body) < 4 {
			return
		}
		replyID, reply = packetid.SbPlayPong, append([]byte(nil), body[:4]...)
	default:
		return
	}
	tc.writeMu.Lock()
	_ = protocol.WritePacket(tc.Conn, replyID, reply, tc.Threshold)
	tc.writeMu.Unlock()
}

// StartDiscardDrain spawns a goroutine that reads every subsequent packet
// off the pipe and throws it away. Use when a test doesn't care about
// packet contents but needs to keep the synchronous net.Pipe drained so
// the server's writer doesn't block (e.g. during MovePlayer flows that emit
// >64 packets per player).
func (tc *Client) StartDiscardDrain() {
	go func() {
		for {
			buf, err := protocol.ReadPacket(tc.Conn, tc.Threshold)
			if err != nil {
				return
			}
			if id, err := protocol.ReadVarInt(buf); err == nil {
				tc.AutoReply(id, buf.Bytes())
			}
		}
	}()
}

// StartDrain spawns a goroutine that reads every subsequent packet off the
// pipe and pushes its ID onto the returned channel. Closes the channel when
// the pipe errors out (e.g. test cleanup). Use after login when later
// server-side writes (announceJoin, broadcasts) would otherwise deadlock
// the synchronous net.Pipe.
func (tc *Client) StartDrain() <-chan int {
	ids := make(chan int, 64)
	go func() {
		defer close(ids)
		for {
			buf, err := protocol.ReadPacket(tc.Conn, tc.Threshold)
			if err != nil {
				return
			}
			id, err := protocol.ReadVarInt(buf)
			if err != nil {
				return
			}
			tc.AutoReply(id, buf.Bytes())
			ids <- id
		}
	}()
	return ids
}

// Read reads the next packet (2 s timeout), answering it like AutoReply.
func (tc *Client) Read(t *testing.T) (id int, payload *bytes.Buffer) {
	t.Helper()
	_ = tc.Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf, err := protocol.ReadPacket(tc.Conn, tc.Threshold)
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	_ = tc.Conn.SetReadDeadline(time.Time{})
	id, err = protocol.ReadVarInt(buf)
	if err != nil {
		t.Fatalf("decode packet id: %v", err)
	}
	tc.AutoReply(id, buf.Bytes())
	return id, buf
}

// Handshake builds a Handshake payload.
func Handshake(protoVer int32, addr string, port uint16, nextState int32) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteVarInt32(protoVer))
	buf.Write(protocol.WriteString(addr))
	buf.WriteByte(byte(port >> 8))
	buf.WriteByte(byte(port & 0xff))
	buf.Write(protocol.WriteVarInt32(nextState))
	return buf.Bytes()
}

// Login drives the client through Handshake + Login Start and drains Set
// Compression + Login Success + the join sequence up to the combat
// attributes. After it returns the connection is in the play state on the
// server side and the player is registered; the join announcements
// (Player Info Update / Spawn Player) are the next thing on the wire.
func (tc *Client) Login(t *testing.T, name string) {
	t.Helper()
	tc.Write(t, packetid.SbHandshake, Handshake(763, "localhost", 25565, 2))
	tc.Write(t, packetid.SbLoginStart, protocol.WriteString(name))

	id, body := tc.Read(t)
	if id != packetid.CbLoginSetCompr {
		t.Fatalf("expected Set Compression (0x%02X), got 0x%02X", packetid.CbLoginSetCompr, id)
	}
	thr, err := protocol.ReadVarInt(body)
	if err != nil {
		t.Fatal(err)
	}
	tc.Threshold = thr

	if id, _ := tc.Read(t); id != packetid.CbLoginSuccess {
		t.Fatalf("expected LoginSuccess (0x%02X), got 0x%02X", packetid.CbLoginSuccess, id)
	}
	// Drain Login(Play) + chunks + any world-state Block Updates + SyncPos
	// + DeclareCommands. Block Update count varies per test (some pre-seed
	// blocks into the hub world), so we consume packets until SyncPos
	// arrives, then read one more for the Declare Commands packet that
	// follows it.
	for {
		id, _ := tc.Read(t)
		if id == packetid.CbPlaySyncPos {
			break
		}
	}
	if id, _ := tc.Read(t); id != packetid.CbPlayDeclareCommands {
		t.Fatalf("expected DeclareCommands after SyncPos, got 0x%02X", id)
	}
	// Combat Attributes (attack-speed for the cooldown bar) trails Declare
	// Commands in the join sequence.
	if id, _ := tc.Read(t); id != packetid.CbPlayUpdateAttributes {
		t.Fatalf("expected UpdateAttributes after DeclareCommands, got 0x%02X", id)
	}
}

// WaitFor polls until cond returns true or the deadline passes. Useful for
// observing async server-side effects.
func WaitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

// DrainExpect reads the next packet IDs off ch (a StartDrain channel) and
// asserts each matches the expected sequence. Times out if anything stalls.
func DrainExpect(t *testing.T, ch <-chan int, who string, expected ...int) {
	t.Helper()
	for i, want := range expected {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("%s: channel closed waiting for packet %d (want 0x%02X)", who, i, want)
			}
			if got != want {
				t.Errorf("%s: packet %d: got 0x%02X, want 0x%02X", who, i, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: timed out waiting for packet %d (want 0x%02X)", who, i, want)
		}
	}
}

// JoinPrelude is the post-Respawn triplet the server emits before
// streaming chunks: Set Default Spawn Position, Set Center Chunk, and the
// "start waiting for chunks" Game Event.
func JoinPrelude() []int {
	return []int{packetid.CbPlaySpawnPos, packetid.CbPlaySetCenterChunk, packetid.CbPlayGameEvent}
}

// Chunks is n × Chunk Data ids, for splicing into a DrainExpect list.
func Chunks(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = packetid.CbPlayChunkData
	}
	return out
}
