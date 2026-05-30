package server

import (
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
	"sync/atomic"
	"testing"
	"time"
)

// recordingLogic captures hook invocations so we can assert what fired.
type recordingLogic struct {
	game.NoopLogic
	startCalls atomic.Int64
	endCalls   atomic.Int64
	joinCalls  atomic.Int64
	tickCalls  atomic.Int64

	lastJoinName atomic.Value // string
}

func (r *recordingLogic) OnInstanceStart(*game.Ctx) { r.startCalls.Add(1) }
func (r *recordingLogic) OnInstanceEnd(*game.Ctx)   { r.endCalls.Add(1) }
func (r *recordingLogic) OnPlayerJoin(_ *game.Ctx, p game.PlayerHandle) {
	r.joinCalls.Add(1)
	r.lastJoinName.Store(p.Name())
}
func (r *recordingLogic) OnTick(_ *game.Ctx, _ uint64) { r.tickCalls.Add(1) }

func TestAttachLogicFiresOnInstanceStartImmediately(t *testing.T) {
	s := New()
	inst := NewInstance("test", s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	logic := &recordingLogic{}
	s.AttachLogic(inst, logic)
	if logic.startCalls.Load() != 1 {
		t.Errorf("OnInstanceStart: got %d, want 1", logic.startCalls.Load())
	}
}

func TestAttachLogicWiresOnTick(t *testing.T) {
	s := New()
	inst := NewInstance("test", s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)
	logic := &recordingLogic{}
	s.AttachLogic(inst, logic)

	time.Sleep(150 * time.Millisecond) // ~3 ticks
	if logic.tickCalls.Load() < 2 {
		t.Errorf("OnTick: got %d, want >= 2", logic.tickCalls.Load())
	}
}

func TestAttachLogicWiresOnPlayerJoin(t *testing.T) {
	s := New()
	s.Ops.Add("Joiner")

	inst := NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(inst)
	t.Cleanup(func() { _ = s.RemoveInstance("arena") })

	logic := &recordingLogic{}
	s.AttachLogic(inst, logic)

	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Joiner")
	_ = cli.startDrain()

	// Move into the wired instance — that fires OnPlayerJoin on its logic.
	cli.write(t, SbPlayChatCommand, protocol.WriteString("instance join arena"))

	waitFor(t, time.Second, func() bool { return logic.joinCalls.Load() == 1 },
		"OnPlayerJoin to fire")
	if name, _ := logic.lastJoinName.Load().(string); name != "Joiner" {
		t.Errorf("OnPlayerJoin saw name %q, want Joiner", name)
	}
}

func TestStopFiresOnInstanceEnd(t *testing.T) {
	s := New()
	inst := NewInstance("test", s, world.NewMemoryWorld())
	logic := &recordingLogic{}
	s.AttachLogic(inst, logic)

	inst.Stop()
	if logic.endCalls.Load() != 1 {
		t.Errorf("OnInstanceEnd: got %d, want 1 after Stop", logic.endCalls.Load())
	}
}

func TestStartGameUsesRegisteredDefinition(t *testing.T) {
	// Register a tiny game definition for the duration of the test.
	game.Register(&game.Definition{
		ID:         "stub",
		Name:       "Stub",
		MinPlayers: 1,
		MaxPlayers: 4,
		Template: func() *world.Template {
			t := world.NewTemplate()
			t.SetBlock(world.Position{X: 0, Y: 60, Z: 0}, world.Stone)
			return t
		}(),
		New: func() game.Logic { return &recordingLogic{} },
	})

	s := New()
	inst, err := s.StartGame("stub")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.RemoveInstance(inst.ID) })

	// Template block should be present in the new instance's world.
	if got := inst.World.GetBlock(world.Position{X: 0, Y: 60, Z: 0}); got != world.Stone {
		t.Errorf("stone from template: got %+v, want Stone", got)
	}
	// Instance ID should be uniquified with a serial.
	if inst.ID == "stub" {
		t.Errorf("expected uniquified ID, got bare %q", inst.ID)
	}
}

// playerBridge / instanceBridge surface checks — make sure the wrappers
// expose what the interfaces claim without panic.
func TestPlayerBridgeSurface(t *testing.T) {
	s := New()
	inst := NewInstance("test", s, world.NewMemoryWorld())
	t.Cleanup(inst.Stop)

	c := &ClientConnection{
		server:   s,
		instance: inst,
		player: &player.Player{
			EntityID: 7,
			Name:     "Wrapped",
		},
	}
	var h game.PlayerHandle = playerBridge{conn: c}
	if h.Name() != "Wrapped" {
		t.Errorf("Name: got %q", h.Name())
	}
	if h.EntityID() != 7 {
		t.Errorf("EntityID: got %d", h.EntityID())
	}
	// Pose just snapshots — safe even with zero state.
	_ = h.Pose()
}
