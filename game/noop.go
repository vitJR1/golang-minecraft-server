package game

import "minecraft-server/world"

// NoopLogic implements Logic with no-op defaults. Embed it in your own
// Logic value to avoid stubbing every method you don't care about:
//
//	type myGame struct {
//	    game.NoopLogic
//	    score map[int32]int
//	}
//
//	func (g *myGame) OnPlayerJoin(c *game.Ctx, p game.PlayerHandle) {
//	    g.score[p.EntityID()] = 0
//	}
//
// OnBlockBreak/OnBlockPlace return true (allow). OnChat returns the
// message unchanged with allow=true.
type NoopLogic struct{}

func (NoopLogic) OnInstanceStart(*Ctx) {}
func (NoopLogic) OnInstanceEnd(*Ctx)   {}

func (NoopLogic) OnPlayerJoin(*Ctx, PlayerHandle)  {}
func (NoopLogic) OnPlayerLeave(*Ctx, PlayerHandle) {}

func (NoopLogic) OnTick(*Ctx, uint64) {}

func (NoopLogic) OnBlockBreak(*Ctx, PlayerHandle, world.Position) bool { return true }
func (NoopLogic) OnBlockPlace(*Ctx, PlayerHandle, world.Position, world.Block) bool {
	return true
}

func (NoopLogic) OnChat(_ *Ctx, _ PlayerHandle, msg string) (string, bool) {
	return msg, true
}

func (NoopLogic) OnPlayerAttack(*Ctx, PlayerHandle, PlayerHandle) bool { return true }

func (NoopLogic) OnPlayerDeath(*Ctx, PlayerHandle, PlayerHandle) {}
