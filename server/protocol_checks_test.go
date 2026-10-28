package server

import (
	"testing"
	"time"

	"minecraft-server/anticheat"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// protocol_checks_test.go: anticheat.md P2 #18 sprint, #19 chat/commands,
// #20 drop flood.

// chatPacket is a Chat Message body: the text (the signature fields after
// it aren't read by the server).
func chatPacket(text string) []byte { return protocol.WriteString(text) }

// --- #19 chat ------------------------------------------------------------

func TestChatWithFormattingCodesDropped(t *testing.T) {
	inst := bareInstance(New(), world.NewMemoryWorld())
	var got []string
	inst.OnChat = func(_ *ClientConnection, msg string) (string, bool) {
		got = append(got, msg)
		return msg, true
	}
	c := offlineConn(inst, "Forger", player.Survival, 0, 64, 0)

	play(t, c, SbPlayChatMessage, chatPacket("hello"))
	play(t, c, SbPlayChatMessage, chatPacket("§4[Admin]§r you are banned"))
	play(t, c, SbPlayChatMessage, chatPacket("hi\n<Server> fake line"))
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("delivered %q, want only the plain message", got)
	}
	if !flaggedNow(c, "badpackets.chat") {
		t.Fatal("formatting codes not flagged")
	}
}

func TestCommandWithControlCharactersNotRun(t *testing.T) {
	s := New()
	c := offlineConn(s.Hub, "Injector", player.Survival, 0, 64, 0)
	s.Ops.Add("Injector") // /tp would work if run
	c.player.MoveTo(0, 64, 0, true)
	play(t, c, SbPlayChatCommand, protocol.WriteString("tp 100 64 100\x00"))
	if x := c.player.Snapshot().X; x != 0 {
		t.Fatalf("command with a NUL ran: x=%v", x)
	}
	if !flaggedNow(c, "badpackets.chat") {
		t.Fatal("not flagged")
	}
}

func TestChatSpamKicked(t *testing.T) {
	_, cli, conn := loginOne(t, "Spammer")
	for i := 0; i < int(anticheat.ChatBurst)+5 && !conn.isClosed(); i++ {
		cli.tryWrite(SbPlayChatMessage, chatPacket("buy gold"))
	}
	waitFor(t, 2*time.Second, conn.isClosed, "spammer to be kicked")
}

func TestChatBurstWithinBudgetAllowed(t *testing.T) {
	_, cli, conn := loginOne(t, "Talker")
	for i := 0; i < int(anticheat.ChatBurst)-1; i++ {
		cli.write(t, SbPlayChatMessage, chatPacket("gg"))
	}
	barrier(t, cli, conn)
	if conn.isClosed() {
		t.Fatal("a burst under vanilla's limit was kicked")
	}
}

func TestOpsExemptFromChatSpam(t *testing.T) {
	s := New()
	s.Ops.Add("Admin")
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Admin")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Admin")
	for i := 0; i < 3*int(anticheat.ChatBurst); i++ {
		cli.write(t, SbPlayChatMessage, chatPacket("announcement"))
	}
	barrier(t, cli, conn)
	if conn.isClosed() {
		t.Fatal("an op was kicked for spam")
	}
}

// --- #20 drops ------------------------------------------------------------

func TestDropFloodCapped(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	c := offlineConn(inst, "Dropper", player.Survival, 0.5, 64, 0.5)
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(hotbarStart, itemStack{ID: iron, Count: 64})

	for i := 0; i < 64; i++ {
		dropAction(t, c, 4) // Q: one unit
	}
	dropped := itemsIn(inst)[iron]
	if dropped < int(anticheat.DropBurst) || dropped > int(anticheat.DropBurst)+2 {
		t.Fatalf("dropped %d in a burst, want about %v", dropped, anticheat.DropBurst)
	}
	if left := int(c.inv.get(hotbarStart).Count); left+dropped != 64 {
		t.Fatalf("items lost: %d dropped + %d left != 64", dropped, left)
	}
}

func TestWindowThrowsShareDropBudget(t *testing.T) {
	inst := bareInstance(New(), tntWorld())
	c := offlineConn(inst, "Thrower", player.Survival, 0.5, 64, 0.5)
	iron, _ := world.ItemByName("minecraft:iron_ingot")
	c.inv.set(mainInvStart, itemStack{ID: iron, Count: 64})
	for i := 0; i < 64; i++ {
		clickIn(t, c, 0, int16(mainInvStart), 0, clickThrow)
	}
	dropped := itemsIn(inst)[iron]
	if dropped > int(anticheat.DropBurst)+2 || dropped+int(c.inv.get(mainInvStart).Count) != 64 {
		t.Fatalf("window throws: dropped %d, left %d", dropped, c.inv.get(mainInvStart).Count)
	}
}

// --- #18 sprint ----------------------------------------------------------

func TestSprintHitEndsSprint(t *testing.T) {
	inst := pvpInstance()
	a := offlineConn(inst, "Sprinter", player.Survival, 0.5, 64, 0.5)
	v := offlineConn(inst, "Target", player.Survival, 2.5, 64, 0.5)
	a.ac.Sprinting.Store(true)
	attack(t, a, v)
	if a.ac.Sprinting.Load() {
		t.Fatal("a sprint hit must end the sprint (no KeepSprint)")
	}
}

func TestSprintBonusNeedsPlausibleSprint(t *testing.T) {
	c := &ClientConnection{player: player.New(1, "Runner", [16]byte{})}
	c.ac.Sprinting.Store(true)
	if !c.sprintHit() {
		t.Fatal("a plain sprint should earn the bonus")
	}
	c.ac.Sneaking.Store(true)
	if c.sprintHit() {
		t.Fatal("no sprint bonus while sneaking")
	}
	c.ac.Sneaking.Store(false)
	c.using.Store(&useState{})
	if c.sprintHit() {
		t.Fatal("no sprint bonus while eating / drawing a bow")
	}
}
