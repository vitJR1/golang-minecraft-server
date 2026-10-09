package server

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// testListener is a server-wide listener that only acts while armed, so the
// global registration (listeners can't be unregistered) doesn't leak into
// other tests in the package.
type testListener struct {
	game.NoopLogic
	armed atomic.Bool

	starts, joins, leaves, deaths atomic.Int32
	interacts                     atomic.Int32
	consumePos                    world.Position // interact clicks here are consumed
	vetoBreaks                    atomic.Bool
	lastChatRewrite               string
}

func (l *testListener) OnInstanceStart(*game.Ctx) {
	if l.armed.Load() {
		l.starts.Add(1)
	}
}
func (l *testListener) OnPlayerJoin(*game.Ctx, game.PlayerHandle) {
	if l.armed.Load() {
		l.joins.Add(1)
	}
}
func (l *testListener) OnPlayerLeave(*game.Ctx, game.PlayerHandle) {
	if l.armed.Load() {
		l.leaves.Add(1)
	}
}
func (l *testListener) OnBlockInteract(_ *game.Ctx, _ game.PlayerHandle, click game.BlockInteraction) bool {
	if !l.armed.Load() {
		return true
	}
	l.interacts.Add(1)
	return click.Pos != l.consumePos
}
func (l *testListener) OnBlockBreak(*game.Ctx, game.PlayerHandle, world.Position) bool {
	return !(l.armed.Load() && l.vetoBreaks.Load())
}
func (l *testListener) OnChat(_ *game.Ctx, _ game.PlayerHandle, msg string) (string, bool) {
	if !l.armed.Load() {
		return msg, true
	}
	return "[L] " + msg, true
}

var sharedListener = &testListener{consumePos: world.Position{X: 7, Y: 70, Z: 7}}

func init() {
	game.RegisterListener("test-listener", sharedListener)
}

func armListener(t *testing.T) *testListener {
	t.Helper()
	l := sharedListener
	l.starts.Store(0)
	l.joins.Store(0)
	l.leaves.Store(0)
	l.deaths.Store(0)
	l.interacts.Store(0)
	l.vetoBreaks.Store(false)
	l.armed.Store(true)
	t.Cleanup(func() { l.armed.Store(false) })
	return l
}

func TestListenerSeesEveryInstance(t *testing.T) {
	l := armListener(t)
	s := New() // hub → OnInstanceStart
	inst := NewInstance("arena", s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	if got := l.starts.Load(); got != 2 {
		t.Errorf("OnInstanceStart: got %d, want 2 (hub + arena)", got)
	}

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Alice")
	cli.startDiscardDrain()
	waitFor(t, time.Second, func() bool { return l.joins.Load() == 1 }, "listener OnPlayerJoin")

	// The instance's own hook still runs, after the listener.
	var hubHook atomic.Int32
	s.Hub.OnPlayerLeave = func(*ClientConnection) { hubHook.Add(1) }
	_ = cli.conn.Close()
	waitFor(t, time.Second, func() bool { return l.leaves.Load() == 1 && hubHook.Load() == 1 },
		"listener + instance OnPlayerLeave")
}

func TestListenerConsumesBlockInteract(t *testing.T) {
	l := armListener(t)
	s := New()
	s.Hub.World.SetBlock(world.Position{X: 7, Y: 70, Z: 7}, world.Stone)
	s.Hub.World.SetBlock(world.Position{X: 8, Y: 70, Z: 8}, world.Stone)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Digger")
	cli.startDiscardDrain()

	dig := func(x, y, z int, action int32) {
		var p bytes.Buffer
		protocol.WriteVarInt32ToBuffer(&p, action)
		p.Write(protocol.WritePosition(x, y, z))
		p.WriteByte(1)
		protocol.WriteVarInt32ToBuffer(&p, 1)
		cli.write(t, SbPlayPlayerAction, p.Bytes())
	}

	// Consumed click: block stays, even after the survival "finished
	// digging" follow-up for the same block.
	dig(7, 70, 7, 0)
	dig(7, 70, 7, 2)
	waitFor(t, time.Second, func() bool { return l.interacts.Load() == 1 }, "OnBlockInteract for consumed click")
	time.Sleep(50 * time.Millisecond)
	if got := s.Hub.World.GetBlock(world.Position{X: 7, Y: 70, Z: 7}); got != world.Stone {
		t.Errorf("consumed click broke the block: %+v", got)
	}

	// Ordinary click: passes through to the break.
	dig(8, 70, 8, 0)
	waitFor(t, time.Second, func() bool {
		return s.Hub.World.GetBlock(world.Position{X: 8, Y: 70, Z: 8}) == world.Air
	}, "unconsumed click to break the block")
}

func TestListenerVetoRunsBeforeInstanceHook(t *testing.T) {
	l := armListener(t)
	l.vetoBreaks.Store(true)
	s := New()
	inst := NewInstance("t", s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	var hookRan atomic.Bool
	inst.OnBlockBreak = func(*ClientConnection, world.Position) bool { hookRan.Store(true); return true }
	c := &ClientConnection{server: s, instance: inst}
	c.player = player.New(1, "X", [16]byte{})
	if inst.allowBlockBreak(c, world.Position{}) {
		t.Error("listener veto must win")
	}
	if hookRan.Load() {
		t.Error("instance hook must not run after a listener veto")
	}
	l.vetoBreaks.Store(false)
	if !inst.allowBlockBreak(c, world.Position{}) || !hookRan.Load() {
		t.Error("without veto the instance hook decides")
	}
}

func TestListenerChatRewriteComposes(t *testing.T) {
	armListener(t)
	s := New()
	inst := NewInstance("t", s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	inst.OnChat = func(_ *ClientConnection, msg string) (string, bool) { return msg + "!", true }
	c := &ClientConnection{server: s, instance: inst}
	c.player = player.New(1, "X", [16]byte{})
	got, allow := inst.filterChat(c, "hi")
	if !allow || got != "[L] hi!" {
		t.Errorf("filterChat = %q,%v", got, allow)
	}
}

func TestPluginCommandDispatch(t *testing.T) {
	var gotCtx *game.Ctx
	var gotArgs []string
	var gotPlayer string
	game.RegisterCommand(&game.Command{
		Name: "/ptest", Aliases: []string{"ptest-alias"},
		Help: "//ptest — test",
		Run: func(ctx *game.Ctx, p game.PlayerHandle, args []string) {
			gotCtx, gotArgs, gotPlayer = ctx, args, p.Name()
		},
		Complete: func(_ *game.Ctx, _ game.PlayerHandle, args []string) []string {
			if len(args) == 1 {
				return []string{"foo", "bar"}
			}
			return nil
		},
	})
	game.RegisterCommand(&game.Command{
		Name: "op", Help: "shadowed by the built-in /op",
		Run: func(*game.Ctx, game.PlayerHandle, []string) { t.Error("shadowed plugin command must not run") },
	})

	s := New()
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Cmd")
	cli.startDiscardDrain()
	c := findConn(t, s, "Cmd")

	// As the vanilla client sends "//ptest a b": "/ptest a b".
	s.RunCommand(c, "/ptest a b")
	if gotCtx == nil || gotCtx.InstanceID != s.Hub.ID || gotPlayer != "Cmd" || strings.Join(gotArgs, " ") != "a b" {
		t.Errorf("dispatch: ctx=%v player=%q args=%v", gotCtx, gotPlayer, gotArgs)
	}
	gotArgs = nil
	s.RunCommand(c, "PTEST-ALIAS z")
	if strings.Join(gotArgs, " ") != "z" {
		t.Errorf("alias dispatch: %v", gotArgs)
	}
	// A client that includes the slash still reaches a plain-named command.
	s.RunCommand(c, "/help")

	// Built-in wins over the shadowing plugin command (its Run would t.Error).
	s.Ops.Add("Cmd")
	s.RunCommand(c, "op Someone")

	// Visible in the command tree / help and tab-completes via Complete.
	var seen bool
	for _, cmd := range commandsVisibleTo(c) {
		if cmd.Name == "/ptest" {
			seen = true
		}
	}
	if !seen {
		t.Error("plugin command missing from commandsVisibleTo")
	}
	// The server prefix-filters whatever Complete returns.
	_, _, matches := s.Suggestions(c, "//ptest fo")
	if len(matches) != 1 || matches[0] != "foo" {
		t.Errorf("plugin completion: %v", matches)
	}
	_, _, matches = s.Suggestions(c, "//ptest ")
	if len(matches) != 2 {
		t.Errorf("plugin completion on empty word: %v", matches)
	}
}

func TestSectionBlocksPayloads(t *testing.T) {
	changes := []world.BlockChange{
		{Pos: world.Position{X: 1, Y: 65, Z: 2}, Block: world.Stone},
		{Pos: world.Position{X: 1, Y: 65, Z: 2}, Block: world.Air}, // last write wins
		{Pos: world.Position{X: -1, Y: -3, Z: 17}, Block: world.Stone},
		{Pos: world.Position{X: 3, Y: 70, Z: 2}, Block: world.Stone}, // same section as the first
	}
	payloads := sectionBlocksPayloads(changes)
	if len(payloads) != 2 {
		t.Fatalf("payloads: %d, want 2 sections", len(payloads))
	}

	// Sorted by chunk X: the negative-X section comes first.
	r := bytes.NewBuffer(payloads[0])
	var pos int64
	if err := binaryReadLong(r, &pos); err != nil {
		t.Fatal(err)
	}
	if pos != packSectionPos(sectionKey{X: -1, Y: -1, Z: 1}) {
		t.Errorf("section pos: %x", pos)
	}
	n, _ := protocol.ReadVarInt(r)
	if n != 1 {
		t.Errorf("entries: %d, want 1", n)
	}
	v, _ := protocol.ReadVarLong(r)
	wantLocal := int64(15)<<8 | int64(1)<<4 | int64(13) // x=-1&15=15, z=17&15=1, y=-3&15=13
	if v != int64(world.Stone.StateID)<<12|wantLocal {
		t.Errorf("entry: %x, want %x", v, int64(world.Stone.StateID)<<12|wantLocal)
	}

	r = bytes.NewBuffer(payloads[1])
	_ = binaryReadLong(r, &pos)
	if pos != packSectionPos(sectionKey{X: 0, Y: 4, Z: 0}) {
		t.Errorf("section pos: %x", pos)
	}
	n, _ = protocol.ReadVarInt(r)
	if n != 2 {
		t.Errorf("entries: %d, want 2 (deduped position + second block)", n)
	}
}

func TestPackSectionPos(t *testing.T) {
	// From wiki.vg: x/z 22 bits, y 20 bits, two's complement.
	if got := packSectionPos(sectionKey{X: 1, Y: -1, Z: -1}); got != (1<<42)|(0x3FFFFF<<20)|0xFFFFF {
		t.Errorf("packSectionPos: %x", got)
	}
}

func binaryReadLong(r *bytes.Buffer, out *int64) error {
	var b [8]byte
	if _, err := r.Read(b[:]); err != nil {
		return err
	}
	*out = int64(uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7]))
	return nil
}
