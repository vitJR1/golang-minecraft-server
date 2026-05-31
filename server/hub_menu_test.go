package server

import (
	"bytes"
	"minecraft-server/protocol"
	"testing"
	"time"
)

// skipPackets reads-and-discards n packets, failing the test on read
// errors. Used in this file to push past the post-login join broadcasts
// (PlayerInfoUpdate + Set Container Slot for the blaze rod) before
// asserting menu wire output.
func skipPackets(t *testing.T, cli *testClient, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		cli.read(t)
	}
}

// TestUseItemOpensMainMenu: a player in hub right-clicks (SbPlayUseItem)
// and we expect Open Screen + Set Container Content to land on the wire.
func TestUseItemOpensMainMenu(t *testing.T) {
	s := New()
	SetupHubMenu(s)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Picker")

	// completeOfflineLogin drains the login + chunk sequence. The hub's
	// OnPlayerJoin hook then fires PlayerInfoUpdate (announceJoin) +
	// Set Container Slot (blaze rod). Drain both before triggering the
	// menu so our assertions land on the menu packets, not the join noise.
	skipPackets(t, cli, 2)

	// Send Use Item: hand=0, sequence=0.
	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	cli.write(t, SbPlayUseItem, p.Bytes())

	// Expect Open Screen, then Set Container Content.
	if id, _ := cli.read(t); id != CbPlayOpenScreen {
		t.Fatalf("expected OpenScreen 0x%02X, got 0x%02X", CbPlayOpenScreen, id)
	}
	if id, _ := cli.read(t); id != CbPlaySetContainerContent {
		t.Fatalf("expected SetContainerContent 0x%02X, got 0x%02X",
			CbPlaySetContainerContent, id)
	}

	// Server-side: menu should be tracked as "main" with 3 entries.
	conn := findConn(t, s, "Picker")
	m := conn.menu.Load()
	if m == nil {
		t.Fatal("menu not set on connection")
	}
	if m.kind != "main" {
		t.Errorf("menu kind: got %q, want main", m.kind)
	}
	if len(m.entries) != 3 {
		t.Errorf("menu entries: got %d, want 3", len(m.entries))
	}
}

// TestClickGameTeleportsToLobby: clicking an icon in the main menu
// (FFA / BedWars / SkyWars) MoveablyMoves the player into the matching
// lobby instance and clears the server-side menu state.
func TestClickGameTeleportsToLobby(t *testing.T) {
	s := New()
	SetupLobbies(s)
	SetupHubMenu(s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Clicker")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Clicker")

	// Open the main menu server-side (skip the wire round-trip).
	conn.openHubMainMenu()

	// Click slot 2 = FFA.
	var p bytes.Buffer
	p.WriteByte(menuWindowID)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteShort(2))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteEmptySlot())
	cli.write(t, SbPlayClickContainer, p.Bytes())

	waitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("Clicker")
		return ok && inst != nil && inst.ID == LobbyFFA
	}, "Clicker to be teleported into the FFA lobby")
	if m := conn.menu.Load(); m != nil {
		t.Errorf("menu state should be cleared after teleport, got %+v", m)
	}
}

// TestCloseContainerClearsMenu: pressing E (or any close) drops the
// server's menu state.
func TestCloseContainerClearsMenu(t *testing.T) {
	s := New()
	SetupHubMenu(s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Closer")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Closer")

	conn.openHubMainMenu()
	if conn.menu.Load() == nil {
		t.Fatal("precondition: menu should be open")
	}

	cli.write(t, SbPlayCloseContainer, []byte{menuWindowID})
	waitFor(t, time.Second, func() bool { return conn.menu.Load() == nil }, "menu to clear on close")
}
