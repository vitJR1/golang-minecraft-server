package server

import (
	"bytes"
	"encoding/json"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"net"
	"sync/atomic"
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

// startDiscardDrain spawns a goroutine that reads every subsequent packet
// off the pipe and throws it away. Use when a test doesn't care about
// packet contents but needs to keep the synchronous net.Pipe drained so
// the server's writerLoop doesn't block (e.g. during MovePlayer flows
// that emit >64 packets per player).
func (tc *testClient) startDiscardDrain() {
	go func() {
		for {
			if _, err := protocol.ReadPacket(tc.conn, tc.threshold); err != nil {
				return
			}
		}
	}()
}

// startDrain spawns a goroutine that reads every subsequent packet off the
// pipe and pushes its ID onto the returned channel. Closes the channel when
// the pipe errors out (e.g. test cleanup). Use after login when later
// server-side writes (announceJoin, broadcasts) would otherwise deadlock
// the synchronous net.Pipe.
func (tc *testClient) startDrain() <-chan int {
	ids := make(chan int, 64)
	go func() {
		defer close(ids)
		for {
			buf, err := protocol.ReadPacket(tc.conn, tc.threshold)
			if err != nil {
				return
			}
			id, err := protocol.ReadVarInt(buf)
			if err != nil {
				return
			}
			ids <- id
		}
	}()
	return ids
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

	// Play packets in order:
	//   Login(Play), SpawnPos, SetCenterChunk, GameEvent (start_waiting),
	//   16 × ChunkData, SyncPos.
	type expect struct {
		name   string
		id     int
		minLen int // payload byte floor — Login(Play) carries the ~70KB registry codec
	}
	expected := []expect{
		{"Login(Play)", CbPlayLogin, 1000},
		{"SpawnPos", CbPlaySpawnPos, 8},             // packed Position + Float angle
		{"SetCenterChunk", CbPlaySetCenterChunk, 2}, // two VarInts
		{"GameEvent(13)", CbPlayGameEvent, 5},       // 1 byte id + 4 byte float
	}
	for i := 0; i < spawnChunkCount; i++ {
		expected = append(expected, expect{"ChunkData", CbPlayChunkData, 100})
	}
	expected = append(expected, expect{"SyncPos", CbPlaySyncPos, 30})
	for _, want := range expected {
		got, body := cli.read(t)
		if got != want.id {
			t.Fatalf("%s: id 0x%02X, want 0x%02X", want.name, got, want.id)
		}
		if body.Len() < want.minLen {
			t.Errorf("%s: payload %d bytes, want >= %d", want.name, body.Len(), want.minLen)
		}
	}

	// announceJoin sends a PlayerInfo with just self (the newcomer joined
	// alone here). Drain it so the test client's cleanup doesn't strand a
	// pending server-side write.
	if id, _ := cli.read(t); id != CbPlayPlayerInfoUpdate {
		t.Errorf("expected post-login PlayerInfoUpdate, got 0x%02X", id)
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
	// Drain Login(Play) + chunks + any world-state Block Updates + SyncPos.
	// Block Update count varies per test (some pre-seed blocks into the hub
	// world), so we just consume packets until SyncPos arrives — that's the
	// last packet in the join sequence before announceJoin starts emitting
	// PlayerInfoUpdate / SpawnPlayer.
	for {
		id, _ := cli.read(t)
		if id == CbPlaySyncPos {
			return
		}
	}
}

// spawnChunkCount mirrors server.HubChunkRadius. Used by tests that walk
// the post-login (and post-Respawn) packet sequence and need to know how
// many ChunkData packets to skip.
var spawnChunkCount = int(HubChunkRadius * 2 * HubChunkRadius * 2)

// loginChunks returns spawnChunkCount × CbPlayChunkData IDs, suitable for
// splicing into a drainExpect expectation list.
func loginChunks() []int {
	out := make([]int, spawnChunkCount)
	for i := range out {
		out[i] = CbPlayChunkData
	}
	return out
}

// joinPrelude is the post-Respawn triplet the server emits before
// streaming chunks: Set Default Spawn Position, Set Center Chunk, and
// the "start waiting for chunks" Game Event. Splice it into drainExpect
// lists between Respawn and loginChunks().
func joinPrelude() []int {
	return []int{CbPlaySpawnPos, CbPlaySetCenterChunk, CbPlayGameEvent}
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
	completeOfflineLogin(t, cli1, "Alice")
	aliceCh := cli1.startDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	bobCh := cli2.startDrain()

	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 2 },
		"Players.Count == 2")
	if _, ok := s.Hub.Players.ByName("Alice"); !ok {
		t.Error("Alice missing from PlayerList")
	}
	if _, ok := s.Hub.Players.ByName("Bob"); !ok {
		t.Error("Bob missing from PlayerList")
	}

	// Drain the join announces before the chat broadcast so we know exactly
	// what to expect afterward.
	drainExpect(t, aliceCh, "Alice pre-chat",
		CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)
	drainExpect(t, bobCh, "Bob pre-chat",
		CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)

	chat := append(protocol.WriteString(`{"text":"hello"}`), 0) // overlay = false
	s.Hub.Players.Broadcast(CbPlaySystemChat, chat, -1)

	drainExpect(t, aliceCh, "Alice chat", CbPlaySystemChat)
	drainExpect(t, bobCh, "Bob chat", CbPlaySystemChat)
}

// drainExpect reads the next N packet IDs and asserts each matches the
// expected sequence. Times out if anything stalls.
func drainExpect(t *testing.T, ch <-chan int, who string, expected ...int) {
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

func TestPlayerListBroadcastExceptSkipsSender(t *testing.T) {
	s := New()

	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Alice")
	aliceCh := cli1.startDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	bobCh := cli2.startDrain()

	drainExpect(t, aliceCh, "Alice pre-chat",
		CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)
	drainExpect(t, bobCh, "Bob pre-chat",
		CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)

	alice, _ := s.Hub.Players.ByName("Alice")

	payload := append(protocol.WriteString(`{"text":"from alice"}`), 0)
	s.Hub.Players.Broadcast(CbPlaySystemChat, payload, alice.player.EntityID)

	drainExpect(t, bobCh, "Bob chat", CbPlaySystemChat)

	// Alice was excluded — drain channel should stay empty for a short window.
	select {
	case id := <-aliceCh:
		t.Errorf("Alice received id 0x%02X but should have been excluded", id)
	case <-time.After(150 * time.Millisecond):
		// expected
	}
}

func TestPlayerListRemovesOnDisconnect(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "TempPlayer")
	_ = cli.startDrain() // unblocks joinAndAnnounce's PlayerInfo write

	// Players.Add now happens inside joinAndAnnounce (after sendPlayPackets'
	// 40ms of inter-packet sleeps), so the count won't be 1 immediately —
	// wait until the join settles.
	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 1 },
		"Players.Count == 1")

	_ = cli.conn.Close()

	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 0 },
		"PlayerList to drain after client disconnect")
}

func TestTwoPlayersSeeEachOther(t *testing.T) {
	s := New()

	// Alice joins alone. Drain her own PlayerInfo before connecting Bob, so
	// Alice's joinAndAnnounce is provably done before Bob's runs — the test
	// then sees a deterministic sequence of packets for each.
	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Alice")
	aliceCh := cli1.startDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", CbPlayPlayerInfoUpdate)

	// Bob joins. Alice should see Bob spawn; Bob should see Alice spawn.
	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	bobCh := cli2.startDrain()

	drainExpect(t, aliceCh, "Alice",
		CbPlayPlayerInfoUpdate, // Bob added to tab
		CbPlaySpawnPlayer,      // Bob spawned
	)
	drainExpect(t, bobCh, "Bob",
		CbPlayPlayerInfoUpdate, // tab list (Alice + Bob)
		CbPlaySpawnPlayer,      // Alice spawned for Bob
	)
}

func TestPlayerMovementBroadcasts(t *testing.T) {
	s := New()

	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Alice")
	aliceCh := cli1.startDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	bobCh := cli2.startDrain()

	drainExpect(t, aliceCh, "Alice pre-move",
		CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)
	drainExpect(t, bobCh, "Bob pre-move",
		CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)

	// Alice sends a position update. Bob should see Teleport Entity for her.
	var move bytes.Buffer
	move.Write(protocol.WriteDouble(5.5))
	move.Write(protocol.WriteDouble(70))
	move.Write(protocol.WriteDouble(-3.25))
	move.WriteByte(1) // on ground
	cli1.write(t, SbPlaySetPos, move.Bytes())

	drainExpect(t, bobCh, "Bob sees Alice move", CbPlayTeleportEntity)

	// Alice should not echo her own movement back.
	select {
	case id := <-aliceCh:
		t.Errorf("Alice should not receive her own movement, got 0x%02X", id)
	case <-time.After(150 * time.Millisecond):
		// expected
	}
}

func TestPlayerLeaveDespawnsForOthers(t *testing.T) {
	s := New()

	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Alice")
	aliceCh := cli1.startDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	_ = cli2.startDrain()

	drainExpect(t, aliceCh, "Alice pre-leave",
		CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)

	// Bob disconnects.
	_ = cli2.conn.Close()

	drainExpect(t, aliceCh, "Alice sees Bob leave",
		CbPlayRemoveEntities, CbPlayPlayerInfoRemove)
}

func TestBlockPlaceBroadcastsAndAcks(t *testing.T) {
	s := New()

	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Placer")
	aliceCh := cli1.startDrain()
	drainExpect(t, aliceCh, "Placer (solo bootstrap)", CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Watcher")
	bobCh := cli2.startDrain()
	drainExpect(t, aliceCh, "Placer pre-place", CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)
	drainExpect(t, bobCh, "Watcher pre-place", CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)

	// Placer right-clicks the top face of block (0, 63, 0) → server places
	// at (0, 64, 0).
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0)     // hand
	p.Write(protocol.WritePosition(0, 63, 0)) // clicked block
	protocol.WriteVarInt32ToBuffer(&p, 1)     // face: +Y (top)
	p.Write(protocol.WriteFloat(0.5))         // cursor x
	p.Write(protocol.WriteFloat(1.0))         // cursor y
	p.Write(protocol.WriteFloat(0.5))         // cursor z
	p.WriteByte(0)                            // inside_block = false
	protocol.WriteVarInt32ToBuffer(&p, 42)    // sequence
	cli1.write(t, SbPlayUseItemOnBlock, p.Bytes())

	// Watcher receives Block Update (or — depending on ordering — the
	// Placer's own ack also goes out as Block Update to Watcher first).
	drainExpect(t, bobCh, "Watcher sees block place", CbPlayBlockUpdate)
	// Placer receives Ack then Block Update (server.SetBlock broadcasts to
	// all including the placer; the placer's prediction is already
	// confirmed by the ack).
	drainExpect(t, aliceCh, "Placer Ack + own update",
		CbPlayAckBlockChange, CbPlayBlockUpdate)

	// World state reflects the placement.
	if got := s.Hub.World.GetBlock(world.Position{X: 0, Y: 64, Z: 0}); got != world.Stone {
		t.Errorf("world: got %+v, want Stone", got)
	}
}

func TestBlockBreakClearsAndAcks(t *testing.T) {
	s := New()
	s.Hub.World.SetBlock(world.Position{X: 5, Y: 70, Z: 5}, world.Stone)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Breaker")
	ch := cli.startDrain()
	// World-state replay (Block Update for the pre-seeded stone) is now
	// drained inside completeOfflineLogin since it lands before SyncPos.
	// Only the join announce remains visible here.
	drainExpect(t, ch, "Breaker bootstrap", CbPlayPlayerInfoUpdate)

	// Player Action: started digging (action=0) at (5,70,5), face=1, seq=7.
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0) // action: started digging
	p.Write(protocol.WritePosition(5, 70, 5))
	p.WriteByte(1)                        // face
	protocol.WriteVarInt32ToBuffer(&p, 7) // sequence
	cli.write(t, SbPlayPlayerAction, p.Bytes())

	drainExpect(t, ch, "Ack + Block Update",
		CbPlayAckBlockChange, CbPlayBlockUpdate)

	if got := s.Hub.World.GetBlock(world.Position{X: 5, Y: 70, Z: 5}); got != world.Air {
		t.Errorf("world: got %+v, want Air after break", got)
	}
}

func TestSwingArmBroadcasts(t *testing.T) {
	s := New()
	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Swinger")
	aliceCh := cli1.startDrain()
	drainExpect(t, aliceCh, "Swinger solo", CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bystander")
	bobCh := cli2.startDrain()
	drainExpect(t, aliceCh, "Swinger pre-swing", CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)
	drainExpect(t, bobCh, "Bystander pre-swing", CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)

	cli1.write(t, SbPlaySwingArm, protocol.WriteVarInt32(0)) // main hand
	drainExpect(t, bobCh, "Bystander sees animation", CbPlayEntityAnimation)

	// Self does not see own animation.
	select {
	case id := <-aliceCh:
		t.Errorf("Swinger should not see own animation, got 0x%02X", id)
	case <-time.After(150 * time.Millisecond):
		// expected
	}
}

func TestChatBroadcasts(t *testing.T) {
	s := New()
	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Speaker")
	aliceCh := cli1.startDrain()
	drainExpect(t, aliceCh, "Speaker solo", CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Listener")
	bobCh := cli2.startDrain()
	drainExpect(t, aliceCh, "Speaker pre-chat", CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)
	drainExpect(t, bobCh, "Listener pre-chat", CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)

	// 1.20.1 ChatMessage payload is String + Long(timestamp) + Long(salt)
	// + Optional sig + VarInt(msg count) + FixedBitSet(20). We only need
	// the leading string for the handler.
	cli1.write(t, SbPlayChatMessage, protocol.WriteString("hello world"))

	// Both clients should receive the broadcast.
	drainExpect(t, aliceCh, "Speaker sees own chat", CbPlaySystemChat)
	drainExpect(t, bobCh, "Listener sees chat", CbPlaySystemChat)
}

func TestOpCommand(t *testing.T) {
	s := New()
	// Seed Speaker as an op so they can /op someone else.
	s.Ops.Add("Boss")

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Boss")
	ch := cli.startDrain()
	drainExpect(t, ch, "Boss solo", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("op Notch"))
	drainExpect(t, ch, "Op confirm to sender", CbPlaySystemChat)

	if !s.Ops.Has("Notch") {
		t.Error("Notch should now be op")
	}
}

func TestNonOpDeniedCommand(t *testing.T) {
	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Rando")
	ch := cli.startDrain()
	drainExpect(t, ch, "Rando solo", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("op Notch"))
	drainExpect(t, ch, "Permission denied reply", CbPlaySystemChat)

	if s.Ops.Has("Notch") {
		t.Error("Non-op shouldn't be able to /op")
	}
}

func TestOnBlockPlaceVetoRollsBack(t *testing.T) {
	s := New()
	var hookCalls atomic.Int64
	s.Hub.OnBlockPlace = func(c *ClientConnection, pos world.Position, b world.Block) bool {
		hookCalls.Add(1)
		return false
	}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Placer")
	ch := cli.startDrain()
	drainExpect(t, ch, "Placer solo", CbPlayPlayerInfoUpdate)

	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WritePosition(0, 63, 0))
	protocol.WriteVarInt32ToBuffer(&p, 1) // face +Y
	p.Write(protocol.WriteFloat(0.5))
	p.Write(protocol.WriteFloat(1.0))
	p.Write(protocol.WriteFloat(0.5))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 7)
	cli.write(t, SbPlayUseItemOnBlock, p.Bytes())

	// Ack first, then the corrective Block Update with the prior block.
	drainExpect(t, ch, "Placer ack + rollback", CbPlayAckBlockChange, CbPlayBlockUpdate)

	if hookCalls.Load() != 1 {
		t.Errorf("hook calls: got %d, want 1", hookCalls.Load())
	}
	if got := s.Hub.World.GetBlock(world.Position{X: 0, Y: 64, Z: 0}); got != world.Air {
		t.Errorf("world should be unchanged: got %+v, want Air", got)
	}
}

func TestOnBlockBreakVetoRollsBack(t *testing.T) {
	s := New()
	s.Hub.World.SetBlock(world.Position{X: 5, Y: 70, Z: 5}, world.Stone)
	var hookCalls atomic.Int64
	s.Hub.OnBlockBreak = func(c *ClientConnection, pos world.Position) bool {
		hookCalls.Add(1)
		return false
	}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Breaker")
	ch := cli.startDrain()
	drainExpect(t, ch, "Breaker bootstrap", CbPlayPlayerInfoUpdate)

	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WritePosition(5, 70, 5))
	p.WriteByte(1)
	protocol.WriteVarInt32ToBuffer(&p, 9)
	cli.write(t, SbPlayPlayerAction, p.Bytes())

	drainExpect(t, ch, "Breaker ack + rollback", CbPlayAckBlockChange, CbPlayBlockUpdate)

	if hookCalls.Load() != 1 {
		t.Errorf("hook calls: got %d, want 1", hookCalls.Load())
	}
	if got := s.Hub.World.GetBlock(world.Position{X: 5, Y: 70, Z: 5}); got != world.Stone {
		t.Errorf("world should still have Stone: got %+v", got)
	}
}

func TestOnChatRewrite(t *testing.T) {
	s := New()
	s.Hub.OnChat = func(c *ClientConnection, msg string) (string, bool) {
		return "[censored]", true
	}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Speaker")
	// Drain own PlayerInfoUpdate from announceJoin so the next read picks
	// up the chat broadcast. No startDrain here — we need cli.read to
	// inspect the actual payload, and the drainer goroutine would race.
	if id, _ := cli.read(t); id != CbPlayPlayerInfoUpdate {
		t.Fatalf("expected own PlayerInfoUpdate, got 0x%02X", id)
	}

	cli.write(t, SbPlayChatMessage, protocol.WriteString("naughty text"))

	id, payload := cli.read(t)
	if id != CbPlaySystemChat {
		t.Fatalf("expected SystemChat (0x%02X), got 0x%02X", CbPlaySystemChat, id)
	}
	jsonStr, err := protocol.ReadStringFromBuf(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(jsonStr), []byte("censored")) {
		t.Errorf("chat should be rewritten: %q", jsonStr)
	}
	if bytes.Contains([]byte(jsonStr), []byte("naughty")) {
		t.Errorf("original text leaked: %q", jsonStr)
	}
}

func TestOnChatVeto(t *testing.T) {
	s := New()
	s.Hub.OnChat = func(c *ClientConnection, msg string) (string, bool) {
		return "", false
	}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Silent")
	ch := cli.startDrain()
	drainExpect(t, ch, "Silent solo", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatMessage, protocol.WriteString("anything"))

	select {
	case id := <-ch:
		t.Errorf("vetoed chat still emitted packet 0x%02X", id)
	case <-time.After(150 * time.Millisecond):
		// expected
	}
}

func TestOnPlayerJoinHook(t *testing.T) {
	s := New()
	var joined atomic.Int64
	var joinedName atomic.Value
	s.Hub.OnPlayerJoin = func(c *ClientConnection) {
		joined.Add(1)
		joinedName.Store(c.player.Name)
	}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Newbie")
	_ = cli.startDrain()
	waitFor(t, time.Second, func() bool { return joined.Load() == 1 },
		"OnPlayerJoin to fire")

	if got, _ := joinedName.Load().(string); got != "Newbie" {
		t.Errorf("OnPlayerJoin saw name %q, want Newbie", got)
	}
}

func TestOnPlayerLeaveHook(t *testing.T) {
	s := New()
	var left atomic.Int64
	s.Hub.OnPlayerLeave = func(c *ClientConnection) {
		left.Add(1)
	}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Quitter")
	_ = cli.startDrain()
	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 1 },
		"player in list")

	_ = cli.conn.Close()
	waitFor(t, time.Second, func() bool { return left.Load() == 1 },
		"OnPlayerLeave to fire")
}

func TestMovePlayerSwitchesInstances(t *testing.T) {
	s := New()
	arena := NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)
	t.Cleanup(arena.Stop)

	// Alice in arena is op so she can run /instance.
	s.Ops.Add("Mover")

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Mover")
	ch := cli.startDrain()
	drainExpect(t, ch, "Mover solo in hub", CbPlayPlayerInfoUpdate)

	// Trigger the move.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance join arena"))

	// Wire-level sequence for the move.
	drainExpect(t, ch, "move pre-chunks",
		append([]int{
			CbPlayPlayerInfoRemove, // clear hub tab list
			CbPlayRespawn,          // tell client to wipe world + entities
		}, joinPrelude()...)...,
	)
	drainExpect(t, ch, "move chunks", loginChunks()...)
	drainExpect(t, ch, "move post-chunks",
		CbPlaySyncPos,          // arena spawn point
		CbPlayPlayerInfoUpdate, // arena tab list (just Mover)
		CbPlaySystemChat,       // "Moved to arena"
	)

	// Server-side: player is now in arena.
	conn, inst, ok := s.FindPlayer("Mover")
	if !ok {
		t.Fatal("Mover not found after move")
	}
	if inst != arena {
		t.Errorf("expected Mover in arena, got %s", inst.ID)
	}
	if conn.instance != arena {
		t.Errorf("conn.instance: got %s, want arena", conn.instance.ID)
	}
	if s.Hub.Players.Count() != 0 {
		t.Errorf("hub should be empty: count = %d", s.Hub.Players.Count())
	}
	if arena.Players.Count() != 1 {
		t.Errorf("arena should have 1 player: count = %d", arena.Players.Count())
	}
}

func TestMovePlayerNotifiesStayers(t *testing.T) {
	s := New()
	arena := NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)
	t.Cleanup(arena.Stop)
	s.Ops.Add("Leaver")

	// Stayer joins hub, gets a drainer set up before Leaver joins.
	stayer := pipeClientOn(t, s)
	completeOfflineLogin(t, stayer, "Stayer")
	stayerCh := stayer.startDrain()
	drainExpect(t, stayerCh, "Stayer solo bootstrap", CbPlayPlayerInfoUpdate)

	// Leaver joins hub.
	leaver := pipeClientOn(t, s)
	completeOfflineLogin(t, leaver, "Leaver")
	leaverCh := leaver.startDrain()

	// Both clients see each other appear.
	drainExpect(t, stayerCh, "Stayer sees Leaver join",
		CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)
	drainExpect(t, leaverCh, "Leaver bootstrap",
		CbPlayPlayerInfoUpdate, CbPlaySpawnPlayer)

	// Leaver moves out — Stayer should see despawn first.
	leaver.write(t, SbPlayChatCommand, protocol.WriteString("instance join arena"))

	// Stayer receives RemoveEntities + PI Remove for Leaver (in that order,
	// per LeaveAndAnnounce).
	drainExpect(t, stayerCh, "Stayer sees Leaver leave",
		CbPlayRemoveEntities, CbPlayPlayerInfoRemove)

	// Leaver sees the full move sequence.
	drainExpect(t, leaverCh, "Leaver move pre-chunks",
		append([]int{
			CbPlayPlayerInfoRemove,
			CbPlayRespawn,
		}, joinPrelude()...)...,
	)
	drainExpect(t, leaverCh, "Leaver move chunks", loginChunks()...)
	drainExpect(t, leaverCh, "Leaver move post-chunks",
		CbPlaySyncPos,
		CbPlayPlayerInfoUpdate,
		CbPlaySystemChat,
	)
}

func TestMoveToSameInstanceIsNoOp(t *testing.T) {
	s := New()
	s.Ops.Add("Player")

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Player")
	ch := cli.startDrain()
	drainExpect(t, ch, "solo bootstrap", CbPlayPlayerInfoUpdate)

	// /instance hub while already in hub — should reply but not move.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance join hub"))
	drainExpect(t, ch, "already-there reply", CbPlaySystemChat)
}

func TestInstanceCreateAndJoin(t *testing.T) {
	s := New()
	s.Ops.Add("Builder")

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Builder")
	ch := cli.startDrain()
	drainExpect(t, ch, "Builder solo", CbPlayPlayerInfoUpdate)

	// Create
	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance create arena"))
	drainExpect(t, ch, "create reply", CbPlaySystemChat)

	if s.GetInstance("arena") == nil {
		t.Fatal("arena not registered after /instance create")
	}
	t.Cleanup(func() { _ = s.RemoveInstance("arena") })

	// Now join it.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance join arena"))
	drainExpect(t, ch, "join pre-chunks",
		append([]int{
			CbPlayPlayerInfoRemove,
			CbPlayRespawn,
		}, joinPrelude()...)...,
	)
	drainExpect(t, ch, "join chunks", loginChunks()...)
	drainExpect(t, ch, "join post-chunks",
		CbPlaySyncPos,
		CbPlayPlayerInfoUpdate,
		CbPlaySystemChat,
	)

	conn, inst, ok := s.FindPlayer("Builder")
	if !ok || inst.ID != "arena" {
		t.Fatalf("Builder should be in arena, got ok=%v inst=%v", ok, inst)
	}
	_ = conn
}

func TestInstanceCreateDuplicateRejected(t *testing.T) {
	s := New()
	s.Ops.Add("Op")
	NewInstance("existing", s, world.NewMemoryWorld())
	s.AddInstance(s.GetInstance("hub")) // no-op, just sanity
	arena := NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)
	t.Cleanup(arena.Stop)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Op")
	ch := cli.startDrain()
	drainExpect(t, ch, "Op solo", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance create arena"))
	drainExpect(t, ch, "duplicate reply", CbPlaySystemChat)

	// Still exactly one arena (hub + arena = 2 total).
	if got := len(s.InstanceIDs()); got != 2 {
		t.Errorf("instance count: got %d, want 2", got)
	}
}

func TestInstanceDeleteEmpty(t *testing.T) {
	s := New()
	s.Ops.Add("Deleter")
	arena := NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Deleter")
	ch := cli.startDrain()
	drainExpect(t, ch, "Deleter solo", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance delete arena"))
	drainExpect(t, ch, "delete reply", CbPlaySystemChat)

	if s.GetInstance("arena") != nil {
		t.Error("arena should be gone after /instance delete")
	}
}

func TestInstanceDeleteHubRejected(t *testing.T) {
	s := New()
	s.Ops.Add("BadActor")

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "BadActor")
	ch := cli.startDrain()
	drainExpect(t, ch, "BadActor solo", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance delete hub"))
	drainExpect(t, ch, "reject reply", CbPlaySystemChat)

	if s.GetInstance("hub") == nil {
		t.Error("hub should still exist")
	}
}

func TestInstanceDeleteSelfMovesToHub(t *testing.T) {
	s := New()
	s.Ops.Add("Solo")
	arena := NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Solo")
	ch := cli.startDrain()
	drainExpect(t, ch, "Solo bootstrap", CbPlayPlayerInfoUpdate)

	// Move to arena.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance join arena"))
	drainExpect(t, ch, "join arena pre-chunks",
		append([]int{CbPlayPlayerInfoRemove, CbPlayRespawn}, joinPrelude()...)...)
	drainExpect(t, ch, "join arena chunks", loginChunks()...)
	drainExpect(t, ch, "join arena post-chunks",
		CbPlaySyncPos, CbPlayPlayerInfoUpdate, CbPlaySystemChat)

	// Delete arena while inside it: server should evac caller to hub, then delete.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance delete arena"))
	drainExpect(t, ch, "evac + delete pre-chunks",
		append([]int{CbPlayPlayerInfoRemove, CbPlayRespawn}, joinPrelude()...)...)
	drainExpect(t, ch, "evac + delete chunks", loginChunks()...)
	drainExpect(t, ch, "evac + delete post-chunks",
		CbPlaySyncPos,
		CbPlayPlayerInfoUpdate, // hub tab list bootstrap
		CbPlaySystemChat,       // "Deleted instance arena"
	)

	if s.GetInstance("arena") != nil {
		t.Error("arena should be gone")
	}
	_, inst, ok := s.FindPlayer("Solo")
	if !ok || inst.ID != "hub" {
		t.Errorf("Solo should be in hub, got ok=%v inst=%v", ok, inst)
	}
}

func TestInstanceCreateFromTemplate(t *testing.T) {
	s := New()
	s.Ops.Add("Builder")

	tmpl := world.NewTemplate()
	tmpl.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Stone)
	tmpl.SetBlock(world.Position{X: 1, Y: 64, Z: 0}, world.Stone)
	tmpl.AddSpawnPoint(world.SpawnPoint{Position: world.Position{X: 0, Y: 65, Z: 0}})
	s.RegisterTemplate("platform", tmpl)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Builder")
	ch := cli.startDrain()
	drainExpect(t, ch, "Builder solo", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance create lab platform"))
	drainExpect(t, ch, "create reply", CbPlaySystemChat)

	inst := s.GetInstance("lab")
	if inst == nil {
		t.Fatal("lab not created")
	}
	t.Cleanup(func() { _ = s.RemoveInstance("lab") })

	// Template blocks should now be in the new instance's world.
	if got := inst.World.GetBlock(world.Position{X: 0, Y: 64, Z: 0}); got != world.Stone {
		t.Errorf("template block (0,64,0): got %+v, want Stone", got)
	}
	if got := inst.World.GetBlock(world.Position{X: 1, Y: 64, Z: 0}); got != world.Stone {
		t.Errorf("template block (1,64,0): got %+v, want Stone", got)
	}
	// Mutating the instance must not affect the template (independence
	// already covered by world tests, but verifying through the create
	// pipeline catches integration regressions).
	inst.World.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Air)
	if got := tmpl.Instantiate().GetBlock(world.Position{X: 0, Y: 64, Z: 0}); got != world.Stone {
		t.Errorf("template leaked: got %+v, want Stone", got)
	}
}

func TestInstanceCreateUnknownTemplateRejected(t *testing.T) {
	s := New()
	s.Ops.Add("Op")

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Op")
	ch := cli.startDrain()
	drainExpect(t, ch, "Op solo", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance create lab nonexistent"))
	drainExpect(t, ch, "rejected reply", CbPlaySystemChat)

	if s.GetInstance("lab") != nil {
		t.Error("lab should not exist when template is unknown")
	}
}

func TestInstanceList(t *testing.T) {
	s := New()
	s.Ops.Add("Lister")
	a := NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(a)
	t.Cleanup(a.Stop)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Lister")
	ch := cli.startDrain()
	drainExpect(t, ch, "Lister solo", CbPlayPlayerInfoUpdate)

	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance list"))
	// Header + 2 entries (hub, arena) = 3 system chat messages.
	drainExpect(t, ch, "list output",
		CbPlaySystemChat, CbPlaySystemChat, CbPlaySystemChat)
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
