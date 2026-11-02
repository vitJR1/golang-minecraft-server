package commands

import (
	"sync/atomic"
	"testing"
	"time"

	"minecraft-server/game"
	"minecraft-server/server"
	"minecraft-server/server/effect"
	"minecraft-server/server/servertest"
	"minecraft-server/world"
)

// login logs name into s over a draining pipe client and returns its
// connection.
func login(t *testing.T, s *server.Server, name string) (*servertest.Client, *server.ClientConnection) {
	t.Helper()
	cli := servertest.Dial(t, s)
	cli.Login(t, name)
	cli.StartDiscardDrain()
	return cli, findConn(t, s, name)
}

// findConn waits for name's connection to register.
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

func TestEffectCommand(t *testing.T) {
	s := server.New()
	s.Ops.Add("Op")
	_, c := login(t, s, "Op")
	s.RunCommand(c, "effect Op haste 2 60")
	if s.Effects.Level(c, effect.Haste) != 2 {
		t.Errorf("/effect did not apply haste II: %d", s.Effects.Level(c, effect.Haste))
	}
	s.RunCommand(c, "effect Op clear")
	if s.Effects.Level(c, effect.Haste) != 0 {
		t.Error("/effect clear did not clear")
	}
}

// joinLogic counts OnPlayerJoin calls.
type joinLogic struct {
	game.NoopLogic
	joins atomic.Int32
	name  atomic.Value
}

func (l *joinLogic) OnPlayerJoin(_ *game.Ctx, p game.PlayerHandle) {
	l.joins.Add(1)
	l.name.Store(p.Name())
}

// /instance join into an instance with attached game logic fires its
// OnPlayerJoin.
func TestInstanceJoinFiresLogicJoin(t *testing.T) {
	s := server.New()
	s.Ops.Add("Joiner")
	inst := server.NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(inst)
	t.Cleanup(func() { _ = s.RemoveInstance("arena") })
	logic := &joinLogic{}
	s.AttachLogic(inst, logic)

	cli, _ := login(t, s, "Joiner")
	cli.Command(t, "instance join arena")
	servertest.WaitFor(t, time.Second, func() bool { return logic.joins.Load() == 1 }, "OnPlayerJoin to fire")
	if name, _ := logic.name.Load().(string); name != "Joiner" {
		t.Errorf("OnPlayerJoin saw name %q, want Joiner", name)
	}
}

// loginChunks is the Chunk Data run of a join or a move (the hub's spawn
// ring).
func loginChunks() []int {
	return servertest.Chunks(int(server.HubChunkRadius * 2 * server.HubChunkRadius * 2))
}

// registerMMGame registers a tiny game definition for one test (ids are
// unique per test: the registry has no reset).
func registerMMGame(t *testing.T, id string, min, max int) {
	t.Helper()
	game.Register(&game.Definition{
		ID:         id,
		Name:       "MM Test " + id,
		MinPlayers: min,
		MaxPlayers: max,
		Template:   world.NewTemplate(),
		New:        func() game.Logic { return game.NoopLogic{} },
	})
}
