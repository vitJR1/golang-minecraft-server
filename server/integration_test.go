package server

import (
	"bytes"
	"encoding/json"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/servertest"
	"minecraft-server/world"
	"sync/atomic"
	"testing"
	"time"
)

// testClient speaks the wire protocol to a HandleConn running over net.Pipe.
// The threshold field mirrors the server's compression state and must be
// updated when the test client receives Set Compression.
// The protocol-level client is server/net/servertest; these keep the names the
// server's tests have always used.
type testClient = servertest.Client

// pipeServer dials a fresh Server — use pipeClientOn to attach several
// clients to one.
func pipeServer(t *testing.T) *testClient { return servertest.Dial(t, New()) }

// pipeClientOn attaches a new pipe-backed client to an existing Server.
func pipeClientOn(t *testing.T, s *Server) *testClient { return servertest.Dial(t, s) }

// completeOfflineLogin: see servertest.Client.Login.
func completeOfflineLogin(t *testing.T, cli *testClient, name string) { cli.Login(t, name) }

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	servertest.WaitFor(t, timeout, cond, msg)
}

func drainExpect(t *testing.T, ch <-chan int, who string, expected ...int) {
	t.Helper()
	servertest.DrainExpect(t, ch, who, expected...)
}

var buildHandshake = servertest.Handshake

// giveHeldItem puts itemName in the player's selected hotbar slot (window-0
// slot 36) via a Set Creative Mode Slot packet, so a subsequent block place
// has something in hand (placement no longer falls back to stone). The
// server only honours that packet in creative, so the player is switched to
// Creative first.
func giveHeldItem(t *testing.T, cli *testClient, conn *ClientConnection, itemName string) {
	t.Helper()
	conn.player.SetGamemode(player.Creative)
	id, ok := world.ItemByName(itemName)
	if !ok {
		t.Fatalf("unknown item %q", itemName)
	}
	var p bytes.Buffer
	p.Write(protocol.WriteShort(36)) // hotbar slot 0 in window 0
	p.Write(protocol.WriteSlot(id, 1))
	cli.Write(t, packetid.SbPlaySetCreativeSlot, p.Bytes())
}

func TestStatusFlow(t *testing.T) {
	cli := pipeServer(t)

	// Handshake → status state
	cli.Write(t, packetid.SbHandshake, buildHandshake(763, "localhost", 25565, 1))

	// Status Request (empty payload)
	cli.Write(t, packetid.SbStatusRequest, nil)

	// Response
	id, payload := cli.Read(t)
	if id != packetid.CbStatusResponse {
		t.Fatalf("status response id: got 0x%02X, want 0x%02X", id, packetid.CbStatusResponse)
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
	cli.Write(t, packetid.SbStatusPing, payloadBytes)

	id, pong := cli.Read(t)
	if id != packetid.CbStatusPong {
		t.Errorf("pong id: got 0x%02X, want 0x%02X", id, packetid.CbStatusPong)
	}
	if !bytes.Equal(pong.Bytes(), payloadBytes) {
		t.Errorf("pong payload: got %x, want %x", pong.Bytes(), payloadBytes)
	}
}

func TestOfflineLoginFlow(t *testing.T) {
	cli := pipeServer(t)

	// Handshake → login state
	cli.Write(t, packetid.SbHandshake, buildHandshake(763, "localhost", 25565, 2))

	// Login Start: just the player name (no UUID in 1.20.1 — that was 1.19.x)
	cli.Write(t, packetid.SbLoginStart, protocol.WriteString("TestPlayer"))

	// Set Compression arrives first (still uncompressed), then everything
	// after uses compressed framing.
	id, body := cli.Read(t)
	if id != packetid.CbLoginSetCompr {
		t.Fatalf("expected Set Compression (0x%02X), got 0x%02X", packetid.CbLoginSetCompr, id)
	}
	gotThreshold, err := protocol.ReadVarInt(body)
	if err != nil {
		t.Fatal(err)
	}
	if gotThreshold != CompressionThreshold {
		t.Errorf("set compression threshold: got %d, want %d", gotThreshold, CompressionThreshold)
	}
	cli.Threshold = gotThreshold

	// Expect Login Success
	id, payload := cli.Read(t)
	if id != packetid.CbLoginSuccess {
		t.Fatalf("expected LoginSuccess (0x%02X), got 0x%02X", packetid.CbLoginSuccess, id)
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
		{"Login(Play)", packetid.CbPlayLogin, 1000},
		{"UpdateTags", packetid.CbPlayUpdateTags, 20},        // fluid water/lava + block climbable
		{"ClearTitles", packetid.CbPlayClearTitles, 1},       // Boolean reset: drop the previous server's title
		{"SpawnPos", packetid.CbPlaySpawnPos, 8},             // packed Position + Float angle
		{"SetCenterChunk", packetid.CbPlaySetCenterChunk, 2}, // two VarInts
		{"GameEvent(13)", packetid.CbPlayGameEvent, 5},       // 1 byte id + 4 byte float
	}
	for i := 0; i < spawnChunkCount; i++ {
		expected = append(expected, expect{"ChunkData", packetid.CbPlayChunkData, 100})
	}
	expected = append(expected,
		expect{"SyncPos", packetid.CbPlaySyncPos, 30},
		expect{"DeclareCommands", packetid.CbPlayDeclareCommands, 5},
		expect{"UpdateAttributes", packetid.CbPlayUpdateAttributes, 5})
	for _, want := range expected {
		got, body := cli.Read(t)
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
	if id, _ := cli.Read(t); id != packetid.CbPlayPlayerInfoUpdate {
		t.Errorf("expected post-login PlayerInfoUpdate, got 0x%02X", id)
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
		out[i] = packetid.CbPlayChunkData
	}
	return out
}

// joinPrelude is the post-Respawn triplet the server emits before
// streaming chunks: Set Default Spawn Position, Set Center Chunk, and
// the "start waiting for chunks" Game Event. Splice it into drainExpect
// lists between Respawn and loginChunks().
func joinPrelude() []int {
	return []int{packetid.CbPlaySpawnPos, packetid.CbPlaySetCenterChunk, packetid.CbPlayGameEvent}
}

func TestPlayerListRegistersAndBroadcasts(t *testing.T) {
	s := New()

	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Alice")
	aliceCh := cli1.StartDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", packetid.CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	bobCh := cli2.StartDrain()

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
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)
	drainExpect(t, bobCh, "Bob pre-chat",
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)

	chat := append(protocol.WriteString(`{"text":"hello"}`), 0) // overlay = false
	s.Hub.Players.Broadcast(packetid.CbPlaySystemChat, chat, -1)

	drainExpect(t, aliceCh, "Alice chat", packetid.CbPlaySystemChat)
	drainExpect(t, bobCh, "Bob chat", packetid.CbPlaySystemChat)
}

func TestPlayerListBroadcastExceptSkipsSender(t *testing.T) {
	s := New()

	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Alice")
	aliceCh := cli1.StartDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", packetid.CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	bobCh := cli2.StartDrain()

	drainExpect(t, aliceCh, "Alice pre-chat",
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)
	drainExpect(t, bobCh, "Bob pre-chat",
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)

	alice, _ := s.Hub.Players.ByName("Alice")

	payload := append(protocol.WriteString(`{"text":"from alice"}`), 0)
	s.Hub.Players.Broadcast(packetid.CbPlaySystemChat, payload, alice.player.EntityID)

	drainExpect(t, bobCh, "Bob chat", packetid.CbPlaySystemChat)

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
	_ = cli.StartDrain() // unblocks joinAndAnnounce's PlayerInfo write

	// Players.Add now happens inside joinAndAnnounce (after sendPlayPackets'
	// 40ms of inter-packet sleeps), so the count won't be 1 immediately —
	// wait until the join settles.
	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 1 },
		"Players.Count == 1")

	_ = cli.Conn.Close()

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
	aliceCh := cli1.StartDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", packetid.CbPlayPlayerInfoUpdate)

	// Bob joins. Alice should see Bob spawn; Bob should see Alice spawn.
	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	bobCh := cli2.StartDrain()

	drainExpect(t, aliceCh, "Alice",
		packetid.CbPlayPlayerInfoUpdate, // Bob added to tab
		packetid.CbPlaySpawnPlayer,      // Bob spawned
		packetid.CbPlaySetEquipment,     // Bob's hands/armor
	)
	drainExpect(t, bobCh, "Bob",
		packetid.CbPlayPlayerInfoUpdate, // tab list (Alice + Bob)
		packetid.CbPlaySpawnPlayer,      // Alice spawned for Bob
		packetid.CbPlaySetEquipment,     // Alice's hands/armor
	)
}

func TestPlayerMovementBroadcasts(t *testing.T) {
	s := New()

	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Alice")
	aliceCh := cli1.StartDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", packetid.CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	bobCh := cli2.StartDrain()

	drainExpect(t, aliceCh, "Alice pre-move",
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)
	drainExpect(t, bobCh, "Bob pre-move",
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)

	// Alice sends a position update. Bob should see Teleport Entity for her.
	// (Creative: a 6-block hop is a test shortcut, not walking speed.)
	findConn(t, s, "Alice").player.SetGamemode(player.Creative)
	var move bytes.Buffer
	move.Write(protocol.WriteDouble(5.5))
	move.Write(protocol.WriteDouble(70))
	move.Write(protocol.WriteDouble(-3.25))
	move.WriteByte(1) // on ground
	cli1.Write(t, packetid.SbPlaySetPos, move.Bytes())

	drainExpect(t, bobCh, "Bob sees Alice move", packetid.CbPlayTeleportEntity)

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
	aliceCh := cli1.StartDrain()
	drainExpect(t, aliceCh, "Alice (solo bootstrap)", packetid.CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bob")
	_ = cli2.StartDrain()

	drainExpect(t, aliceCh, "Alice pre-leave",
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)

	// Bob disconnects.
	_ = cli2.Conn.Close()

	drainExpect(t, aliceCh, "Alice sees Bob leave",
		packetid.CbPlayRemoveEntities, packetid.CbPlayPlayerInfoRemove)
}

func TestBlockPlaceBroadcastsAndAcks(t *testing.T) {
	s := New()

	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Placer")
	aliceCh := cli1.StartDrain()
	drainExpect(t, aliceCh, "Placer (solo bootstrap)", packetid.CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Watcher")
	bobCh := cli2.StartDrain()
	drainExpect(t, aliceCh, "Placer pre-place", packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)
	drainExpect(t, bobCh, "Watcher pre-place", packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)

	// Placer right-clicks the top face of block (0, 63, 0) → server places
	// at (0, 64, 0). They must be holding a block first.
	giveHeldItem(t, cli1, findConn(t, s, "Placer"), "minecraft:stone")
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0)     // hand
	p.Write(protocol.WritePosition(0, 63, 0)) // clicked block
	protocol.WriteVarInt32ToBuffer(&p, 1)     // face: +Y (top)
	p.Write(protocol.WriteFloat(0.5))         // cursor x
	p.Write(protocol.WriteFloat(1.0))         // cursor y
	p.Write(protocol.WriteFloat(0.5))         // cursor z
	p.WriteByte(0)                            // inside_block = false
	protocol.WriteVarInt32ToBuffer(&p, 42)    // sequence
	cli1.Write(t, packetid.SbPlayUseItemOnBlock, p.Bytes())

	// Watcher receives Block Update (or — depending on ordering — the
	// Placer's own ack also goes out as Block Update to Watcher first).
	drainExpect(t, bobCh, "Watcher sees block place", packetid.CbPlayBlockUpdate)
	// Placer receives Ack then Block Update (server.SetBlock broadcasts to
	// all including the placer; the placer's prediction is already
	// confirmed by the ack).
	drainExpect(t, aliceCh, "Placer Ack + own update",
		packetid.CbPlayAckBlockChange, packetid.CbPlayBlockUpdate)

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
	// Creative: "started digging" breaks at once (survival timing is
	// covered in dig_test.go). Stand within dig reach of (5,70,5).
	findConn(t, s, "Breaker").player.SetGamemode(player.Creative)
	findConn(t, s, "Breaker").player.MoveTo(3.5, 69, 3.5, true)
	ch := cli.StartDrain()
	// World-state replay (Block Update for the pre-seeded stone) is now
	// drained inside completeOfflineLogin since it lands before SyncPos.
	// Only the join announce remains visible here.
	drainExpect(t, ch, "Breaker bootstrap", packetid.CbPlayPlayerInfoUpdate)

	// Player Action: started digging (action=0) at (5,70,5), face=1, seq=7.
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0) // action: started digging
	p.Write(protocol.WritePosition(5, 70, 5))
	p.WriteByte(1)                        // face
	protocol.WriteVarInt32ToBuffer(&p, 7) // sequence
	cli.Write(t, packetid.SbPlayPlayerAction, p.Bytes())

	drainExpect(t, ch, "Ack + Block Update",
		packetid.CbPlayAckBlockChange, packetid.CbPlayBlockUpdate)

	if got := s.Hub.World.GetBlock(world.Position{X: 5, Y: 70, Z: 5}); got != world.Air {
		t.Errorf("world: got %+v, want Air after break", got)
	}
}

func TestSwingArmBroadcasts(t *testing.T) {
	s := New()
	cli1 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli1, "Swinger")
	aliceCh := cli1.StartDrain()
	drainExpect(t, aliceCh, "Swinger solo", packetid.CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Bystander")
	bobCh := cli2.StartDrain()
	drainExpect(t, aliceCh, "Swinger pre-swing", packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)
	drainExpect(t, bobCh, "Bystander pre-swing", packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)

	cli1.Write(t, packetid.SbPlaySwingArm, protocol.WriteVarInt32(0)) // main hand
	drainExpect(t, bobCh, "Bystander sees animation", packetid.CbPlayEntityAnimation)

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
	aliceCh := cli1.StartDrain()
	drainExpect(t, aliceCh, "Speaker solo", packetid.CbPlayPlayerInfoUpdate)

	cli2 := pipeClientOn(t, s)
	completeOfflineLogin(t, cli2, "Listener")
	bobCh := cli2.StartDrain()
	drainExpect(t, aliceCh, "Speaker pre-chat", packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)
	drainExpect(t, bobCh, "Listener pre-chat", packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)

	// 1.20.1 ChatMessage payload is String + Long(timestamp) + Long(salt)
	// + Optional sig + VarInt(msg count) + FixedBitSet(20). We only need
	// the leading string for the handler.
	cli1.Write(t, packetid.SbPlayChatMessage, protocol.WriteString("hello world"))

	// Both clients should receive the broadcast.
	drainExpect(t, aliceCh, "Speaker sees own chat", packetid.CbPlaySystemChat)
	drainExpect(t, bobCh, "Listener sees chat", packetid.CbPlaySystemChat)
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
	ch := cli.StartDrain()
	drainExpect(t, ch, "Placer solo", packetid.CbPlayPlayerInfoUpdate)

	giveHeldItem(t, cli, findConn(t, s, "Placer"), "minecraft:stone")
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WritePosition(0, 63, 0))
	protocol.WriteVarInt32ToBuffer(&p, 1) // face +Y
	p.Write(protocol.WriteFloat(0.5))
	p.Write(protocol.WriteFloat(1.0))
	p.Write(protocol.WriteFloat(0.5))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 7)
	cli.Write(t, packetid.SbPlayUseItemOnBlock, p.Bytes())

	// Ack first, then the corrective Block Update with the prior block.
	drainExpect(t, ch, "Placer ack + rollback", packetid.CbPlayAckBlockChange, packetid.CbPlayBlockUpdate)

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
	findConn(t, s, "Breaker").player.SetGamemode(player.Creative)
	findConn(t, s, "Breaker").player.MoveTo(3.5, 69, 3.5, true) // within dig reach
	ch := cli.StartDrain()
	drainExpect(t, ch, "Breaker bootstrap", packetid.CbPlayPlayerInfoUpdate)

	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WritePosition(5, 70, 5))
	p.WriteByte(1)
	protocol.WriteVarInt32ToBuffer(&p, 9)
	cli.Write(t, packetid.SbPlayPlayerAction, p.Bytes())

	drainExpect(t, ch, "Breaker ack + rollback", packetid.CbPlayAckBlockChange, packetid.CbPlayBlockUpdate)

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
	if id, _ := cli.Read(t); id != packetid.CbPlayPlayerInfoUpdate {
		t.Fatalf("expected own PlayerInfoUpdate, got 0x%02X", id)
	}

	cli.Write(t, packetid.SbPlayChatMessage, protocol.WriteString("naughty text"))

	id, payload := cli.Read(t)
	if id != packetid.CbPlaySystemChat {
		t.Fatalf("expected SystemChat (0x%02X), got 0x%02X", packetid.CbPlaySystemChat, id)
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
	ch := cli.StartDrain()
	drainExpect(t, ch, "Silent solo", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatMessage, protocol.WriteString("anything"))

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
	_ = cli.StartDrain()
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
	_ = cli.StartDrain()
	waitFor(t, time.Second, func() bool { return s.PlayerCount() == 1 },
		"player in list")

	_ = cli.Conn.Close()
	waitFor(t, time.Second, func() bool { return left.Load() == 1 },
		"OnPlayerLeave to fire")
}

func TestHandshakeInvalidNextState(t *testing.T) {
	cli := pipeServer(t)

	// nextState = 99 is invalid; server should refuse and close.
	cli.Write(t, packetid.SbHandshake, buildHandshake(763, "localhost", 25565, 99))

	_ = cli.Conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := protocol.ReadPacket(cli.Conn, protocol.CompressionDisabled); err == nil {
		t.Fatal("expected read error after server rejected handshake")
	}
}
