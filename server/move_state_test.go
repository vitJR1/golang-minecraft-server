package server

import (
	"testing"
	"time"

	"minecraft-server/player"
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/gameplay/chest"
	"minecraft-server/server/gameplay/inventory"
	"minecraft-server/world"
)

// twoArenas is a server with two running game instances.
func twoArenas(t *testing.T) (*Server, *Instance, *Instance) {
	t.Helper()
	s := New()
	a := NewInstance("arena-a", s, world.NewMemoryWorld())
	b := NewInstance("arena-b", s, world.NewMemoryWorld())
	t.Cleanup(a.Stop)
	t.Cleanup(b.Stop)
	s.AddInstance(a)
	s.AddInstance(b)
	return s, a, b
}

// TestMoveEndsDigUseAndChest: a dig, a bow draw and an open chest belong to
// the instance they began in — none of them survives a move (a dig timed on
// an older arena's clock used to break obsidian instantly in a younger one,
// and the old chest stayed clickable).
func TestMoveEndsDigUseAndChest(t *testing.T) {
	s, a, b := twoArenas(t)
	c := offlineConn(a, "Mover", player.Survival, 0, 64, 0)
	pos := world.Position{X: 1, Y: 64, Z: 0}
	a.World.SetBlock(pos, world.Obsidian)
	b.World.SetBlock(pos, world.Obsidian)
	c.startDig(pos)
	if c.digPos == nil {
		t.Fatal("dig not started")
	}
	c.startUse("minecraft:golden_apple")
	c.openBlockChest(world.Position{X: 2, Y: 64, Z: 0})
	if c.menu.Load() == nil {
		t.Fatal("chest not open")
	}

	if err := s.MovePlayer(c, b, 0, 64, 0); err != nil {
		t.Fatal(err)
	}
	pkts := drainPackets(t, c)
	if c.digPos != nil || c.using.Load() != nil || c.menu.Load() != nil {
		t.Errorf("after the move: dig %v, use %v, menu %v — all must be gone", c.digPos, c.using.Load(), c.menu.Load())
	}
	if countIDs(pkts, packetid.CbPlayCloseContainer) != 1 {
		t.Error("the chest window must be closed on the client")
	}
	c.finishDig(pos)
	if b.World.GetBlock(pos) != world.Obsidian {
		t.Error("obsidian broke on a dig begun in the other arena")
	}
}

func TestElapsedOnOtherClockIsZero(t *testing.T) {
	a, b := &Instance{}, &Instance{}
	if got := elapsedOn(a, a, 10, 30); got != 20 {
		t.Errorf("same instance: %d, want 20", got)
	}
	if got := elapsedOn(b, a, 10, 30); got != 0 {
		t.Errorf("other instance: %d, want 0", got)
	}
	if got := elapsedOn(a, a, 30, 10); got != 0 {
		t.Errorf("clock behind the start: %d, want 0", got)
	}
}

// TestGameToGameResetsState: a kit and spectator mode from one game don't
// follow the player into the next.
func TestGameToGameResetsState(t *testing.T) {
	s, a, b := twoArenas(t)
	c := offlineConn(a, "Hopper", player.Spectator, 0, 64, 0)
	sword, _ := world.ItemByName("minecraft:diamond_sword")
	c.inv.Set(inventory.HotbarStart, inventory.Stack{ID: sword, Count: 1})

	if err := s.MovePlayer(c, b, 0, 64, 0); err != nil {
		t.Fatal(err)
	}
	drain(c)
	if !c.inv.Get(inventory.HotbarStart).Empty() {
		t.Error("the old game's kit followed the player")
	}
	if gm := c.gamemode(); gm == player.Spectator {
		t.Error("spectator mode followed the player")
	}
}

// TestMoveIntoRemovedInstanceGoesNowhere: once RemoveInstance has taken an
// arena down, a move into it is refused and the player stays put.
func TestMoveIntoRemovedInstanceGoesNowhere(t *testing.T) {
	s, a, b := twoArenas(t)
	c := offlineConn(a, "Late", player.Survival, 0, 64, 0)
	if err := s.RemoveInstance(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MovePlayer(c, b, 0, 64, 0); err == nil {
		t.Fatal("moved into a removed instance")
	}
	if c.instance != a {
		t.Error("player left its instance")
	}
	if b.JoinAndAnnounce(c) || b.Players.Count() != 0 {
		t.Error("a removed instance took a join")
	}
}

// TestBlockChangeDuringStreamIsReplayed: a block that changes while the
// world is being streamed to a joiner reaches it after the join.
func TestBlockChangeDuringStreamIsReplayed(t *testing.T) {
	_, a, _ := twoArenas(t)
	c := offlineConn(a, "Joiner", player.Survival, 0, 64, 0)
	a.Players.Remove(c.player.EntityID) // mid-move: not in Players yet
	drain(c)

	a.beginStream(c)
	a.SetBlock(world.Position{X: 3, Y: 64, Z: 3}, world.Stone)
	if n := countIDs(drainPackets(t, c), packetid.CbPlayBlockUpdate); n != 0 {
		t.Fatalf("update sent before the join: %d", n)
	}
	a.Players.Add(c)
	a.endStream(c)
	if n := countIDs(drainPackets(t, c), packetid.CbPlayBlockUpdate); n != 1 {
		t.Errorf("block updates replayed after the join: %d, want 1", n)
	}
	a.SetBlock(world.Position{X: 4, Y: 64, Z: 3}, world.Stone)
	if n := countIDs(drainPackets(t, c), packetid.CbPlayBlockUpdate); n != 1 {
		t.Errorf("after endStream updates go out directly: %d, want 1", n)
	}
}

// TestChestClickFromAfarCloses: a chest is clickable only within 8 blocks
// (vanilla stillValid); from further away the click closes the window and
// moves nothing.
func TestChestClickFromAfarCloses(t *testing.T) {
	_, a, _ := twoArenas(t)
	c := offlineConn(a, "Walker", player.Survival, 0, 64, 0)
	pos := world.Position{X: 1, Y: 64, Z: 0}
	apple, _ := world.ItemByName("minecraft:apple")
	a.chests.Update(pos, func(s *chest.Slots) { s[0] = inventory.Stack{ID: apple, Count: 3} })
	c.openBlockChest(pos)
	drain(c)

	c.player.MoveTo(20, 64, 0, true)
	clickIn(t, c, menuWindowID, 0, 0, inventory.ClickQuickMove)
	if c.menu.Load() != nil {
		t.Error("the chest stayed open 20 blocks away")
	}
	if c.countItem(apple) != 0 {
		t.Error("items left the chest from 20 blocks away")
	}
}

// TestEndGameLandsOnHubPad: a finished round sends its players to the
// hub's arrival point (the spawn pad), not into the air above it — EndGame
// used (0, 80, 0) while every other way to the hub used the pad.
func TestEndGameLandsOnHubPad(t *testing.T) {
	s := New()
	round := NewGameInstance("round", s, world.NewMemoryWorld())
	t.Cleanup(round.Stop)
	s.AddInstance(round)
	c := offlineConn(round, "Winner", player.Survival, 3, 70, 3)
	instanceBridge{server: s, inst: round}.EndGame()
	waitFor(t, 2*time.Second, func() bool {
		c.moveMu.Lock() // the move runs on EndGame's goroutine
		defer c.moveMu.Unlock()
		return c.instance == s.Hub
	}, "moved to the hub")
	if p, a := c.player.Snapshot(), s.Hub.Arrival; p.X != a.X || p.Y != a.Y || p.Z != a.Z {
		t.Errorf("landed at (%v,%v,%v), want the hub pad (%v,%v,%v)", p.X, p.Y, p.Z, a.X, a.Y, a.Z)
	}
	if round.Arrival != gameArrival {
		t.Errorf("a game instance arrives at %+v, want %+v", round.Arrival, gameArrival)
	}
}
