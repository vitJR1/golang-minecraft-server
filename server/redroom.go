package server

import (
	"math"
	"minecraft-server/world"
	"fmt"
)

const redRoomInstanceID = "redroom"

// redRoomManager holds the single ZombieManager for the RedRoom instance.
// Created once in setupRedRoom and reused across calls to enterRedRoom.
var redRoomManager *ZombieManager

// setupRedRoom returns the existing RedRoom instance or creates a fresh one
// from the "redroom" template. Returns nil if the template is not registered
// (i.e. redroom.schem is missing from schem/templates/).
func setupRedRoom(s *Server) *Instance {
	if inst := s.GetInstance(redRoomInstanceID); inst != nil {
		return inst
	}
	tmpl := s.GetTemplate(redRoomInstanceID)
	if tmpl == nil {
		return nil
	}
	inst := NewInstance(redRoomInstanceID, s, tmpl.Instantiate())

	// RedRoom is a read-only punishment cell — no building, no PvP between
	// players (zombies handle damage through ZombieManager instead).
	inst.OnBlockBreak = func(c *ClientConnection, _ world.Position) bool { return false }
	inst.OnBlockPlace = func(c *ClientConnection, _ world.Position, _ world.Block) bool { return false }
	inst.OnPlayerAttack = func(_, _ *ClientConnection) bool { return false }

	s.AddInstance(inst)

	// Create the ZombieManager once for this instance.
	redRoomManager = NewZombieManager(inst)
	return inst
}

// enterRedRoom moves target into the RedRoom instance, applies regeneration
// and weakness effects, and spawns a ring of zombies around them.
// c is the operator who issued the command (used for server.MovePlayer).
func enterRedRoom(c *ClientConnection, target *ClientConnection, inst *Instance) error {
	if err := c.server.MovePlayer(target, inst, 0.5, 65, 0.5); err != nil {
		return err
	}

	// Regeneration II — keeps the player alive so zombies can torment them
	// indefinitely without a kill. Weakness II — prevents them from fighting back.
	// Both last 10 minutes (12 000 ticks).
	_ = target.SendMobEffect(EffectRegeneration, 1, 12000, false)
	_ = target.SendMobEffect(EffectWeakness, 1, 12000, false)

	// Ensure the manager exists (e.g. if setupRedRoom was called before
	// redRoomManager was initialised in a prior session).
	if redRoomManager == nil {
		redRoomManager = NewZombieManager(inst)
	}

	// Spawn 5 zombies evenly distributed in a 3-block radius ring and
	// register them with the ZombieManager so they track the player each tick.
	ids, err := target.SpawnZombieGroup(0.5, 65, 0.5, 5, 3)
	if err != nil {
		return err
	}
	for i, id := range ids {
		angle := float64(i) * (2 * math.Pi / float64(len(ids)))
		redRoomManager.Add(&Zombie{
			EntityID: id,
			X:        0.5 + math.Cos(angle)*3,
			Y:        65,
			Z:        0.5 + math.Sin(angle)*3,
		})
	}
	return nil
}

func SendToRedRoom(s *Server, c *ClientConnection) error {
	inst := setupRedRoom(s)
	if inst == nil {
		return fmt.Errorf("redroom template not found")
	}
	return enterRedRoom(c, c, inst)
}