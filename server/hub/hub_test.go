package hub

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server"
	"minecraft-server/server/lobby"
	"minecraft-server/server/servertest"
	"minecraft-server/world"
)

// menuWindow is the window id the server opens menus under.
const menuWindow = 1

func findConn(t *testing.T, s *server.Server, name string) *server.ClientConnection {
	t.Helper()
	var c *server.ClientConnection
	servertest.WaitFor(t, time.Second, func() bool {
		conn, _, ok := s.FindPlayer(name)
		c = conn
		return ok
	}, name+" to register")
	return c
}

// The menu icons keep the item ids they had as raw constants.
func TestMenuItemIDs(t *testing.T) {
	for name, want := range map[string]int32{
		"minecraft:blaze_rod": 953, "minecraft:diamond_sword": 797, "minecraft:red_bed": 938,
		"minecraft:feather": 811, "minecraft:compass": 888, "minecraft:ender_pearl": 952,
	} {
		if id, ok := world.ItemByName(name); !ok || id != want {
			t.Errorf("%s = %d %v, want %d", name, id, ok, want)
		}
	}
}

// A player in the hub right-clicks the navigator (Use Item): Open Screen +
// Set Container Content land on the wire.
func TestUseItemOpensMainMenu(t *testing.T) {
	s := server.New()
	Setup(s)
	cli := servertest.Dial(t, s)
	cli.Login(t, "Picker")
	// The join's Player Info Update + the navigator's Set Container Slot.
	cli.Read(t)
	cli.Read(t)
	conn := findConn(t, s, "Picker")

	var p bytes.Buffer
	protocol.WriteVarInt32ToBuffer(&p, 0) // hand
	protocol.WriteVarInt32ToBuffer(&p, 0) // sequence
	cli.Write(t, packetid.SbPlayUseItem, p.Bytes())

	if id, _ := cli.Read(t); id != packetid.CbPlayOpenScreen {
		t.Fatalf("expected OpenScreen 0x%02X, got 0x%02X", packetid.CbPlayOpenScreen, id)
	}
	if id, _ := cli.Read(t); id != packetid.CbPlaySetContainerContent {
		t.Fatalf("expected SetContainerContent 0x%02X, got 0x%02X", packetid.CbPlaySetContainerContent, id)
	}
	if !conn.MenuOpen() {
		t.Fatal("menu not set on connection")
	}
}

// Clicking a game icon in the main menu moves the player into the matching
// lobby and forgets the menu.
func TestClickGameTeleportsToLobby(t *testing.T) {
	s := server.New()
	SetupLobbies(s)
	Setup(s)
	cli := servertest.Dial(t, s)
	cli.Login(t, "Clicker")
	cli.StartDiscardDrain()
	conn := findConn(t, s, "Clicker")

	openMain(conn)
	// Click slot 2 = FFA.
	var p bytes.Buffer
	p.WriteByte(menuWindow)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteShort(2))
	p.WriteByte(0)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	protocol.WriteVarInt32ToBuffer(&p, 0)
	p.Write(protocol.WriteEmptySlot())
	cli.Write(t, packetid.SbPlayClickContainer, p.Bytes())

	servertest.WaitFor(t, 2*time.Second, func() bool {
		_, inst, ok := s.FindPlayer("Clicker")
		return ok && inst != nil && inst.ID == lobby.FFA
	}, "Clicker to be teleported into the FFA lobby")
	if conn.MenuOpen() {
		t.Error("menu state should be cleared after teleport")
	}
}

// Pressing E (or any close) drops the server's menu state.
func TestCloseContainerClearsMenu(t *testing.T) {
	s := server.New()
	Setup(s)
	cli := servertest.Dial(t, s)
	cli.Login(t, "Closer")
	cli.StartDiscardDrain()
	conn := findConn(t, s, "Closer")

	openMain(conn)
	if !conn.MenuOpen() {
		t.Fatal("precondition: menu should be open")
	}
	cli.Write(t, packetid.SbPlayCloseContainer, []byte{menuWindow})
	servertest.WaitFor(t, time.Second, func() bool { return !conn.MenuOpen() }, "menu to clear on close")
}

func TestBedwarsArenaEntries(t *testing.T) {
	kinds := map[string][]string{}
	online := map[string]int{}
	arenas := func(kind string) []string { return kinds[kind] }
	count := func(name string) int { return online[name] }

	// No arenas → just the per-mode "create" buttons (4×4, then 1×1 duel).
	if e := bedwarsArenaEntries(arenas, count); len(e) != 2 ||
		e[0].key != "create:"+bedwarsKindFull || e[1].key != "create:"+bedwarsKindDuel {
		t.Fatalf("empty: %+v", e)
	}

	// A populated arena is listed with its player count; an empty one isn't.
	// Duel arenas are listed too, labelled as duels.
	kinds[bedwarsKindFull] = []string{"bw-1", "bw-2"}
	kinds[bedwarsKindDuel] = []string{"bw-1x1-1"}
	online["bw-1"], online["bw-2"], online["bw-1x1-1"] = 2, 0, 1

	e := bedwarsArenaEntries(arenas, count)
	if e[0].key != "create:"+bedwarsKindFull || e[1].key != "create:"+bedwarsKindDuel {
		t.Errorf("create buttons should stay first, got %+v / %+v", e[0], e[1])
	}
	var foundBw1, foundDuel bool
	for i, ent := range e {
		if ent.item.Slot != i {
			t.Errorf("entry %d sits in slot %d", i, ent.item.Slot)
		}
		switch ent.key {
		case "bw-1":
			foundBw1 = true
			if ent.item.Count != 2 {
				t.Errorf("bw-1 count = %d, want 2", ent.item.Count)
			}
		case "bw-2":
			t.Error("empty arena bw-2 should not be listed")
		case "bw-1x1-1":
			foundDuel = true
			if ent.item.Count != 1 || !strings.Contains(ent.item.Name, "Duel") {
				t.Errorf("duel entry = %+v, want count 1 and a Duel label", ent)
			}
		}
	}
	if !foundBw1 || !foundDuel {
		t.Errorf("listed: bw-1 %v, duel %v", foundBw1, foundDuel)
	}
}
