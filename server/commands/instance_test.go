package commands

import (
	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server"
	"minecraft-server/server/servertest"
	"minecraft-server/world"
	"testing"
)

func TestOpCommand(t *testing.T) {
	s := server.New()
	// Seed Speaker as an op so they can /op someone else.
	s.Ops.Add("Boss")

	cli := servertest.Dial(t, s)
	cli.Login(t, "Boss")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Boss solo", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("op Notch"))
	servertest.DrainExpect(t, ch, "Op confirm to sender", packetid.CbPlaySystemChat)

	if !s.Ops.Has("Notch") {
		t.Error("Notch should now be op")
	}
}

func TestMovePlayerSwitchesInstances(t *testing.T) {
	s := server.New()
	arena := server.NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)
	t.Cleanup(arena.Stop)

	// Alice in arena is op so she can run /instance.
	s.Ops.Add("Mover")

	cli := servertest.Dial(t, s)
	cli.Login(t, "Mover")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Mover solo in hub", packetid.CbPlayPlayerInfoUpdate)

	// Trigger the move.
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance join arena"))

	// Wire-level sequence for the move.
	servertest.DrainExpect(t, ch, "move pre-chunks",
		append([]int{
			packetid.CbPlayPlayerInfoRemove, // clear hub tab list
			packetid.CbPlayRespawn,          // two-hop dimension change (the_end →
			packetid.CbPlayRespawn,          // overworld) to fully wipe the client
		}, servertest.JoinPrelude()...)...,
	)
	servertest.DrainExpect(t, ch, "move chunks", loginChunks()...)
	servertest.DrainExpect(t, ch, "move post-chunks",
		packetid.CbPlaySyncPos,             // arena spawn point
		packetid.CbPlayUpdateAttributes,    // cooldown-bar attribute for the arena
		packetid.CbPlaySetContainerContent, // inventory model re-sent after the move
		packetid.CbPlayPlayerInfoUpdate,    // arena tab list (just Mover)
		packetid.CbPlaySystemChat,          // "Moved to arena"
	)

	// Server-side: player is now in arena.
	conn, inst, ok := s.FindPlayer("Mover")
	if !ok {
		t.Fatal("Mover not found after move")
	}
	if inst != arena {
		t.Errorf("expected Mover in arena, got %s", inst.ID)
	}
	if conn.Instance() != arena {
		t.Errorf("conn.Instance(): got %s, want arena", conn.Instance().ID)
	}
	if s.Hub.Players.Count() != 0 {
		t.Errorf("hub should be empty: count = %d", s.Hub.Players.Count())
	}
	if arena.Players.Count() != 1 {
		t.Errorf("arena should have 1 player: count = %d", arena.Players.Count())
	}
}

func TestMovePlayerNotifiesStayers(t *testing.T) {
	s := server.New()
	arena := server.NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)
	t.Cleanup(arena.Stop)
	s.Ops.Add("Leaver")

	// Stayer joins hub, gets a drainer set up before Leaver joins.
	stayer := servertest.Dial(t, s)
	stayer.Login(t, "Stayer")
	stayerCh := stayer.StartDrain()
	servertest.DrainExpect(t, stayerCh, "Stayer solo bootstrap", packetid.CbPlayPlayerInfoUpdate)

	// Leaver joins hub.
	leaver := servertest.Dial(t, s)
	leaver.Login(t, "Leaver")
	leaverCh := leaver.StartDrain()

	// Both clients see each other appear.
	servertest.DrainExpect(t, stayerCh, "Stayer sees Leaver join",
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)
	servertest.DrainExpect(t, leaverCh, "Leaver bootstrap",
		packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySpawnPlayer, packetid.CbPlaySetEquipment)

	// Leaver moves out — Stayer should see despawn first.
	leaver.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance join arena"))

	// Stayer receives RemoveEntities + PI Remove for Leaver (in that order,
	// per LeaveAndAnnounce).
	servertest.DrainExpect(t, stayerCh, "Stayer sees Leaver leave",
		packetid.CbPlayRemoveEntities, packetid.CbPlayPlayerInfoRemove)

	// Leaver sees the full move sequence.
	servertest.DrainExpect(t, leaverCh, "Leaver move pre-chunks",
		append([]int{
			packetid.CbPlayPlayerInfoRemove,
			packetid.CbPlayRespawn,
			packetid.CbPlayRespawn,
		}, servertest.JoinPrelude()...)...,
	)
	servertest.DrainExpect(t, leaverCh, "Leaver move chunks", loginChunks()...)
	servertest.DrainExpect(t, leaverCh, "Leaver move post-chunks",
		packetid.CbPlaySyncPos,
		packetid.CbPlayUpdateAttributes,
		packetid.CbPlaySetContainerContent,
		packetid.CbPlayPlayerInfoUpdate,
		packetid.CbPlaySystemChat,
	)
}

func TestInstanceCreateAndJoin(t *testing.T) {
	s := server.New()
	s.Ops.Add("Builder")

	cli := servertest.Dial(t, s)
	cli.Login(t, "Builder")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Builder solo", packetid.CbPlayPlayerInfoUpdate)

	// Create
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance create arena"))
	servertest.DrainExpect(t, ch, "create reply", packetid.CbPlaySystemChat)

	if s.GetInstance("arena") == nil {
		t.Fatal("arena not registered after /instance create")
	}
	t.Cleanup(func() { _ = s.RemoveInstance("arena") })

	// Now join it.
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance join arena"))
	servertest.DrainExpect(t, ch, "join pre-chunks",
		append([]int{
			packetid.CbPlayPlayerInfoRemove,
			packetid.CbPlayRespawn,
			packetid.CbPlayRespawn,
		}, servertest.JoinPrelude()...)...,
	)
	servertest.DrainExpect(t, ch, "join chunks", loginChunks()...)
	servertest.DrainExpect(t, ch, "join post-chunks",
		packetid.CbPlaySyncPos,
		packetid.CbPlayUpdateAttributes,
		packetid.CbPlaySetContainerContent,
		packetid.CbPlayPlayerInfoUpdate,
		packetid.CbPlaySystemChat,
	)

	conn, inst, ok := s.FindPlayer("Builder")
	if !ok || inst.ID != "arena" {
		t.Fatalf("Builder should be in arena, got ok=%v inst=%v", ok, inst)
	}
	_ = conn
}

func TestInstanceCreateDuplicateRejected(t *testing.T) {
	s := server.New()
	s.Ops.Add("Op")
	server.NewInstance("existing", s, world.NewMemoryWorld())
	s.AddInstance(s.GetInstance("hub")) // no-op, just sanity
	arena := server.NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)
	t.Cleanup(arena.Stop)

	cli := servertest.Dial(t, s)
	cli.Login(t, "Op")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Op solo", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance create arena"))
	servertest.DrainExpect(t, ch, "duplicate reply", packetid.CbPlaySystemChat)

	// Still exactly one arena (hub + arena = 2 total).
	if got := len(s.InstanceIDs()); got != 2 {
		t.Errorf("instance count: got %d, want 2", got)
	}
}

func TestInstanceDeleteEmpty(t *testing.T) {
	s := server.New()
	s.Ops.Add("Deleter")
	arena := server.NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)

	cli := servertest.Dial(t, s)
	cli.Login(t, "Deleter")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Deleter solo", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance delete arena"))
	servertest.DrainExpect(t, ch, "delete reply", packetid.CbPlaySystemChat)

	if s.GetInstance("arena") != nil {
		t.Error("arena should be gone after /instance delete")
	}
}

func TestInstanceDeleteHubRejected(t *testing.T) {
	s := server.New()
	s.Ops.Add("BadActor")

	cli := servertest.Dial(t, s)
	cli.Login(t, "BadActor")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "BadActor solo", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance delete hub"))
	servertest.DrainExpect(t, ch, "reject reply", packetid.CbPlaySystemChat)

	if s.GetInstance("hub") == nil {
		t.Error("hub should still exist")
	}
}

func TestInstanceDeleteSelfMovesToHub(t *testing.T) {
	s := server.New()
	s.Ops.Add("Solo")
	arena := server.NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(arena)

	cli := servertest.Dial(t, s)
	cli.Login(t, "Solo")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Solo bootstrap", packetid.CbPlayPlayerInfoUpdate)

	// Move to arena.
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance join arena"))
	servertest.DrainExpect(t, ch, "join arena pre-chunks",
		append([]int{packetid.CbPlayPlayerInfoRemove, packetid.CbPlayRespawn, packetid.CbPlayRespawn}, servertest.JoinPrelude()...)...)
	servertest.DrainExpect(t, ch, "join arena chunks", loginChunks()...)
	servertest.DrainExpect(t, ch, "join arena post-chunks",
		packetid.CbPlaySyncPos, packetid.CbPlayUpdateAttributes, packetid.CbPlaySetContainerContent, packetid.CbPlayPlayerInfoUpdate, packetid.CbPlaySystemChat)

	// Delete arena while inside it: server should evac caller to hub, then delete.
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance delete arena"))
	servertest.DrainExpect(t, ch, "evac + delete pre-chunks",
		append([]int{packetid.CbPlayPlayerInfoRemove, packetid.CbPlayRespawn, packetid.CbPlayRespawn}, servertest.JoinPrelude()...)...)
	servertest.DrainExpect(t, ch, "evac + delete chunks", loginChunks()...)
	servertest.DrainExpect(t, ch, "evac + delete post-chunks",
		packetid.CbPlaySyncPos,
		packetid.CbPlayUpdateAttributes,    // cooldown-bar attribute for the hub
		packetid.CbPlaySetHealth,           // hub entry resets the player (resetForHub)
		packetid.CbPlaySetContainerContent, // inventory model re-sent after the move
		packetid.CbPlayPlayerInfoUpdate,    // hub tab list bootstrap
		packetid.CbPlaySystemChat,          // "Deleted instance arena"
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
	s := server.New()
	s.Ops.Add("Builder")

	tmpl := world.NewTemplate()
	tmpl.SetBlock(world.Position{X: 0, Y: 64, Z: 0}, world.Stone)
	tmpl.SetBlock(world.Position{X: 1, Y: 64, Z: 0}, world.Stone)
	tmpl.AddSpawnPoint(world.SpawnPoint{Position: world.Position{X: 0, Y: 65, Z: 0}})
	s.RegisterTemplate("platform", tmpl)

	cli := servertest.Dial(t, s)
	cli.Login(t, "Builder")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Builder solo", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance create lab platform"))
	servertest.DrainExpect(t, ch, "create reply", packetid.CbPlaySystemChat)

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
	s := server.New()
	s.Ops.Add("Op")

	cli := servertest.Dial(t, s)
	cli.Login(t, "Op")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Op solo", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance create lab nonexistent"))
	servertest.DrainExpect(t, ch, "rejected reply", packetid.CbPlaySystemChat)

	if s.GetInstance("lab") != nil {
		t.Error("lab should not exist when template is unknown")
	}
}

func TestInstanceList(t *testing.T) {
	s := server.New()
	s.Ops.Add("Lister")
	a := server.NewInstance("arena", s, world.NewMemoryWorld())
	s.AddInstance(a)
	t.Cleanup(a.Stop)

	cli := servertest.Dial(t, s)
	cli.Login(t, "Lister")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Lister solo", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance list"))
	// Header + 2 entries (hub, arena) = 3 system chat messages.
	servertest.DrainExpect(t, ch, "list output",
		packetid.CbPlaySystemChat, packetid.CbPlaySystemChat, packetid.CbPlaySystemChat)
}

func TestNonOpDeniedCommand(t *testing.T) {
	s := server.New()
	cli := servertest.Dial(t, s)
	cli.Login(t, "Rando")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "Rando solo", packetid.CbPlayPlayerInfoUpdate)

	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("op Notch"))
	servertest.DrainExpect(t, ch, "Permission denied reply", packetid.CbPlaySystemChat)

	if s.Ops.Has("Notch") {
		t.Error("Non-op shouldn't be able to /op")
	}
}

func TestMoveToSameInstanceIsNoOp(t *testing.T) {
	s := server.New()
	s.Ops.Add("Player")

	cli := servertest.Dial(t, s)
	cli.Login(t, "Player")
	ch := cli.StartDrain()
	servertest.DrainExpect(t, ch, "solo bootstrap", packetid.CbPlayPlayerInfoUpdate)

	// /instance hub while already in hub — should reply but not move.
	cli.Write(t, packetid.SbPlayChatCommand, protocol.WriteString("instance join hub"))
	servertest.DrainExpect(t, ch, "already-there reply", packetid.CbPlaySystemChat)
}
