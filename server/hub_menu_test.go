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

// TestClickGameOpensArenaSubmenu: from the main menu, clicking the FFA
// slot opens the 6-arena submenu and updates server-side menu state.
func TestClickGameOpensArenaSubmenu(t *testing.T) {
	s := New()
	SetupHubMenu(s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Clicker")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Clicker")

	// Manually open the main menu (skip the wire round-trip).
	conn.openHubMainMenu()

	// Click slot 2 (FFA icon).
	var p bytes.Buffer
	p.WriteByte(menuWindowID)             // window id
	protocol.WriteVarInt32ToBuffer(&p, 0) // state id
	p.Write(protocol.WriteShort(2))       // slot 2 = FFA
	p.WriteByte(0)                        // button (left)
	protocol.WriteVarInt32ToBuffer(&p, 0) // mode (normal click)
	protocol.WriteVarInt32ToBuffer(&p, 0) // changed slots count = 0
	p.Write(protocol.WriteEmptySlot())    // carried item
	cli.write(t, SbPlayClickContainer, p.Bytes())

	waitFor(t, time.Second, func() bool {
		m := conn.menu.Load()
		return m != nil && m.kind == "ffa"
	}, "menu to switch to ffa")

	if got := len(conn.menu.Load().entries); got != 6 {
		t.Errorf("ffa entries: got %d, want 6", got)
	}
}

// TestClickArenaLogsAndClearsMenu: clicking an arena slot in a sub-menu
// fires hubArenaOnClick which nil's the menu.
func TestClickArenaLogsAndClearsMenu(t *testing.T) {
	s := New()
	SetupHubMenu(s)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Finalist")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Finalist")

	// Manually drop into the FFA arena menu.
	conn.openHubArenaMenu("ffa", ffaArenas, "FFA arenas")

	// Click slot 0 — first arena ("The Pit").
	var p bytes.Buffer
	p.WriteByte(menuWindowID)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteShort(0))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteEmptySlot())
	cli.write(t, SbPlayClickContainer, p.Bytes())

	waitFor(t, time.Second, func() bool { return conn.menu.Load() == nil }, "menu to clear after arena pick")
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
