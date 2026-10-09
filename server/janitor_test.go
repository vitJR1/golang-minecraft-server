package server

import (
	"minecraft-server/world"
	"testing"
	"time"
)

func TestJanitorRemovesExpiredEphemeral(t *testing.T) {
	s := New()
	ghost := NewInstance("ghost", s, world.NewMemoryWorld())
	markEphemeral(ghost, time.Now().Add(-2*emptyInstanceTTL))
	s.AddInstance(ghost)
	keeper := NewInstance("keeper", s, world.NewMemoryWorld()) // e.g. a lobby
	s.AddInstance(keeper)

	s.janitorSweep(time.Now())

	if s.GetInstance("ghost") != nil {
		t.Error("expired empty ephemeral instance should be removed")
	}
	if s.GetInstance("keeper") == nil {
		t.Error("non-ephemeral instance must survive the sweep")
	}
}

func TestJanitorSparesFreshAndOccupied(t *testing.T) {
	s := New()

	// Fresh empty arena: stamped now, TTL not reached — survives.
	fresh := NewInstance("fresh", s, world.NewMemoryWorld())
	markEphemeral(fresh, time.Now())
	s.AddInstance(fresh)
	s.janitorSweep(time.Now())
	if s.GetInstance("fresh") == nil {
		t.Fatal("fresh empty instance should survive until the TTL")
	}

	// Occupied arena with a stale stamp: players reset it.
	occ := NewInstance("occ", s, world.NewMemoryWorld())
	markEphemeral(occ, time.Now().Add(-2*emptyInstanceTTL))
	s.AddInstance(occ)
	cli := pipeClientOn(t, s)
	completeOfflineLogin(t, cli, "Player")
	cli.startDiscardDrain()
	conn := findConn(t, s, "Player")
	occ.Players.Add(conn)

	s.janitorSweep(time.Now())
	if s.GetInstance("occ") == nil {
		t.Fatal("occupied instance must survive the sweep")
	}
	if occ.emptySince.Load() != 0 {
		t.Error("empty stamp should reset while the instance is occupied")
	}

	// Player leaves: first sweep stamps, second past the TTL removes.
	occ.Players.Remove(conn.player.EntityID)
	now := time.Now()
	s.janitorSweep(now)
	if s.GetInstance("occ") == nil {
		t.Fatal("first empty sweep must only stamp, not remove")
	}
	s.janitorSweep(now.Add(emptyInstanceTTL + time.Second))
	if s.GetInstance("occ") != nil {
		t.Error("instance empty for a full TTL should be removed")
	}
}
