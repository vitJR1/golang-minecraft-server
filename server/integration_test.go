package server

import (
	"bytes"
	"encoding/json"
	"minecraft-server/protocol"
	"net"
	"testing"
	"time"
)

// testClient speaks the wire protocol to a HandleConn running over net.Pipe.
// The threshold field mirrors the server's compression state and must be
// updated when the test client receives Set Compression.
type testClient struct {
	conn      net.Conn
	threshold int
	server    *Server // the Server backing this client (handy in multi-client tests)
}

// pipeServer wires HandleConn to one side of a net.Pipe and returns a
// testClient for the other side. Starts with compression disabled. A fresh
// Server is created — use pipeClientOn to attach multiple clients to one.
func pipeServer(t *testing.T) *testClient {
	return pipeClientOn(t, New())
}

// pipeClientOn attaches a new pipe-backed client to an existing Server. Use
// when a test needs two or more clients that share PlayerList/world state.
func pipeClientOn(t *testing.T, s *Server) *testClient {
	t.Helper()
	cli, srv := net.Pipe()

	done := make(chan struct{})
	go func() {
		s.HandleConn(srv)
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
	return &testClient{conn: cli, threshold: protocol.CompressionDisabled, server: s}
}

func (tc *testClient) write(t *testing.T, id int32, payload []byte) {
	t.Helper()
	if err := protocol.WritePacket(tc.conn, id, payload, tc.threshold); err != nil {
		t.Fatalf("write packet 0x%02X: %v", id, err)
	}
}

func (tc *testClient) read(t *testing.T) (id int, payload *bytes.Buffer) {
	t.Helper()
	_ = tc.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf, err := protocol.ReadPacket(tc.conn, tc.threshold)
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	_ = tc.conn.SetReadDeadline(time.Time{})
	id, err = protocol.ReadVarInt(buf)
	if err != nil {
		t.Fatalf("decode packet id: %v", err)
	}
	return id, buf
}

func buildHandshake(protoVer int32, addr string, port uint16, nextState int32) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteVarInt32(protoVer))
	buf.Write(protocol.WriteString(addr))
	buf.WriteByte(byte(port >> 8))
	buf.WriteByte(byte(port & 0xff))
	buf.Write(protocol.WriteVarInt32(nextState))
	return buf.Bytes()
}

func TestStatusFlow(t *testing.T) {
	cli := pipeServer(t)

	// Handshake → status state
	cli.write(t, SbHandshake, buildHandshake(763, "localhost", 25565, 1))

	// Status Request (empty payload)
	cli.write(t, SbStatusRequest, nil)

	// Response
	id, payload := cli.read(t)
	if id != CbStatusResponse {
		t.Fatalf("status response id: got 0x%02X, want 0x%02X", id, CbStatusResponse)
	}
	jsonStr, err := protocol.ReadStringFromBuf(payload)
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &info); err != nil {
		t.Fatalf("status JSON: %v\n%s", err, jsonStr)
	}
	version := info["version"].(map[string]any)
	if version["protocol"].(float64) != 763 {
		t.Errorf("status reports wrong protocol: %v", version["protocol"])
	}
	if version["name"].(string) != "1.20.1" {
		t.Errorf("status reports wrong version name: %v", version["name"])
	}

	// Ping → Pong (server echoes the 8-byte payload)
	payloadBytes := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	cli.write(t, SbStatusPing, payloadBytes)

	id, pong := cli.read(t)
	if id != CbStatusPong {
		t.Errorf("pong id: got 0x%02X, want 0x%02X", id, CbStatusPong)
	}
	if !bytes.Equal(pong.Bytes(), payloadBytes) {
		t.Errorf("pong payload: got %x, want %x", pong.Bytes(), payloadBytes)
	}
}

func TestOfflineLoginFlow(t *testing.T) {
	cli := pipeServer(t)

	// Handshake → login state
	cli.write(t, SbHandshake, buildHandshake(763, "localhost", 25565, 2))

	// Login Start: just the player name (no UUID in 1.20.1 — that was 1.19.x)
	cli.write(t, SbLoginStart, protocol.WriteString("TestPlayer"))

	// Set Compression arrives first (still uncompressed), then everything
	// after uses compressed framing.
	id, body := cli.read(t)
	if id != CbLoginSetCompr {
		t.Fatalf("expected Set Compression (0x%02X), got 0x%02X", CbLoginSetCompr, id)
	}
	gotThreshold, err := protocol.ReadVarInt(body)
	if err != nil {
		t.Fatal(err)
	}
	if gotThreshold != CompressionThreshold {
		t.Errorf("set compression threshold: got %d, want %d", gotThreshold, CompressionThreshold)
	}
	cli.threshold = gotThreshold

	// Expect Login Success
	id, payload := cli.read(t)
	if id != CbLoginSuccess {
		t.Fatalf("expected LoginSuccess (0x%02X), got 0x%02X", CbLoginSuccess, id)
	}
	uuidBytes := payload.Next(16)
	if len(uuidBytes) != 16 {
		t.Fatalf("LoginSuccess UUID: got %d bytes, want 16", len(uuidBytes))
	}
	name, err := protocol.ReadStringFromBuf(payload)
	if err != nil {
		t.Fatal(err)
	}
	if name != "TestPlayer" {
		t.Errorf("LoginSuccess name: got %q, want %q", name, "TestPlayer")
	}
	propsCount, err := protocol.ReadVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	if propsCount != 0 {
		t.Errorf("LoginSuccess properties count: got %d, want 0", propsCount)
	}

	// Then the 3 play packets in this order: Login(Play), ChunkData, SyncPos.
	expected := []struct {
		name   string
		id     int
		minLen int // payload byte floor — Login(Play) carries the ~70KB registry codec
	}{
		{"Login(Play)", CbPlayLogin, 1000},
		{"ChunkData", CbPlayChunkData, 100},
		{"SyncPos", CbPlaySyncPos, 30},
	}
	for _, want := range expected {
		got, body := cli.read(t)
		if got != want.id {
			t.Fatalf("%s: id 0x%02X, want 0x%02X", want.name, got, want.id)
		}
		if body.Len() < want.minLen {
			t.Errorf("%s: payload %d bytes, want >= %d", want.name, body.Len(), want.minLen)
		}
	}
}

// completeOfflineLogin drives the client through Handshake + LoginStart and
// drains Set Compression + LoginSuccess + the three play packets. After
// this returns the connection is in StatePlay on the server side and the
// player is registered in Server.Players.
func completeOfflineLogin(t *testing.T, cli *testClient, name string) {
	t.Helper()
	cli.write(t, SbHandshake, buildHandshake(763, "localhost", 25565, 2))
	cli.write(t, SbLoginStart, protocol.WriteString(name))

	id, body := cli.read(t)
	if id != CbLoginSetCompr {
		t.Fatalf("expected Set Compression (0x%02X), got 0x%02X", CbLoginSetCompr, id)
	}
	thr, err := protocol.ReadVarInt(body)
	if err != nil {
		t.Fatal(err)
	}
	cli.threshold = thr

	if id, _ := cli.read(t); id != CbLoginSuccess {
		t.Fatalf("expected LoginSuccess (0x%02X), got 0x%02X", CbLoginSuccess, id)
	}
	// Login(Play), ChunkData, SyncPos.
	for i := 0; i < 3; i++ {
		cli.read(t)
	}
}

// waitFor polls until cond returns true or the deadline passes. Useful for
// observing async server-side effects from integration tests.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
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

func TestPlayerListRegistersAndBroadcasts(t *testing.T) {
	s := New()
	cli1 := pipeClientOn(t, s)
	cli2 := pipeClientOn(t, s)

	completeOfflineLogin(t, cli1, "Alice")
	completeOfflineLogin(t, cli2, "Bob")

	if got := s.Players.Count(); got != 2 {
		t.Fatalf("Players.Count() = %d, want 2", got)
	}
	if _, ok := s.Players.ByName("Alice"); !ok {
		t.Error("Alice missing from PlayerList")
	}
	if _, ok := s.Players.ByName("Bob"); !ok {
		t.Error("Bob missing from PlayerList")
	}

	// net.Pipe writes are synchronous — readers must be parked before the
	// broadcaster writes, otherwise we self-deadlock.
	ids := make(chan int, 2)
	for _, cli := range []*testClient{cli1, cli2} {
		go func(c *testClient) {
			id, _ := c.read(t)
			ids <- id
		}(cli)
	}

	chat := protocol.WriteString(`{"text":"hello"}`)
	chat = append(chat, 0) // System chat "overlay" boolean
	s.Players.Broadcast(CbPlaySystemChat, chat, -1)

	for i := 0; i < 2; i++ {
		if id := <-ids; id != CbPlaySystemChat {
			t.Errorf("client %d: got id 0x%02X, want 0x%02X", i, id, CbPlaySystemChat)
		}
	}
}

func TestPlayerListBroadcastExceptSkipsSender(t *testing.T) {
	s := New()
	cli1 := pipeClientOn(t, s)
	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Alice")
	completeOfflineLogin(t, cli2, "Bob")

	alice, _ := s.Players.ByName("Alice")

	// Park Bob's reader before broadcasting, otherwise the synchronous
	// net.Pipe write would block the broadcaster.
	bobRead := make(chan int)
	go func() {
		id, _ := cli2.read(t)
		bobRead <- id
	}()

	payload := protocol.WriteString(`{"text":"from alice"}`)
	payload = append(payload, 0)
	s.Players.Broadcast(CbPlaySystemChat, payload, alice.player.EntityID)

	if id := <-bobRead; id != CbPlaySystemChat {
		t.Errorf("Bob: got id 0x%02X, want 0x%02X", id, CbPlaySystemChat)
	}

	// Alice should not receive — short-deadline read should error out.
	_ = cli1.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, err := protocol.ReadPacket(cli1.conn, cli1.threshold); err == nil {
		t.Error("Alice received a packet she should have been excluded from")
	}
}

func TestPlayerListRemovesOnDisconnect(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "TempPlayer")

	if got := s.Players.Count(); got != 1 {
		t.Fatalf("after login: Count() = %d, want 1", got)
	}

	_ = cli.conn.Close()

	waitFor(t, time.Second, func() bool { return s.Players.Count() == 0 },
		"PlayerList to drain after client disconnect")
}

func TestHandshakeInvalidNextState(t *testing.T) {
	cli := pipeServer(t)

	// nextState = 99 is invalid; server should refuse and close.
	cli.write(t, SbHandshake, buildHandshake(763, "localhost", 25565, 99))

	_ = cli.conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := protocol.ReadPacket(cli.conn, protocol.CompressionDisabled); err == nil {
		t.Fatal("expected read error after server rejected handshake")
	}
}
