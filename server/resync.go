package server

import "fmt"

// resyncView wipes everything the client currently sees and rebuilds it from
// the player's CURRENT instance and position. It's the "clear everything the
// client renders" primitive: a full Respawn (which the two-hop sendRespawn
// turns into a real dimension change, so no ghost entities/blocks survive),
// followed by re-streaming chunks, the player's own state (position, health
// bar attribute), the instance's world entities (item frames, villagers), and
// the tab list + Spawn Player for every other player here.
//
// Use it to recover a desynced client without moving them between instances
// (MovePlayer already does the cross-instance version). Must run on the
// player's own readLoop goroutine.
func (c *ClientConnection) resyncView() error {
	if c.player == nil {
		return nil
	}
	s := c.player.Snapshot()

	// 1. Full client wipe (two-hop dimension change → no ghosts left behind).
	if err := c.sendRespawn(); err != nil {
		return fmt.Errorf("resync respawn: %w", err)
	}
	// 2. Re-stream spawn triplet → baked world chunks → position.
	if err := c.sendSetDefaultSpawnPosition(int(s.X), int(s.Y), int(s.Z), 0); err != nil {
		return fmt.Errorf("resync spawn pos: %w", err)
	}
	if err := c.sendSetCenterChunk(0, 0); err != nil {
		return fmt.Errorf("resync center chunk: %w", err)
	}
	if err := c.sendStartWaitingForChunks(); err != nil {
		return fmt.Errorf("resync start waiting: %w", err)
	}
	if err := c.sendWorldChunks(); err != nil {
		return fmt.Errorf("resync chunks: %w", err)
	}
	if err := c.sendSyncPlayerPosition(s.X, s.Y, s.Z, 1); err != nil {
		return fmt.Errorf("resync sync pos: %w", err)
	}
	// 3. The Respawn reset the player's own state on the client — re-send the
	//    hearts and the cooldown-bar attribute.
	_ = c.sendSetHealth(s.Health)
	_ = c.sendCombatAttributes()
	// 4. Re-spawn the instance's world entities (item frames, villagers).
	_ = c.sendWorldEntities()
	// 5. Rebuild the tab list + the other players' entities for this client.
	others := c.instance.Players.snapshot()
	if payload := playerInfoAddPayload(others); payload != nil {
		_ = c.safeWrite(CbPlayPlayerInfoUpdate, payload)
	}
	for _, other := range others {
		if other == c {
			continue
		}
		_ = c.safeWrite(CbPlaySpawnPlayer, spawnPlayerPayload(other.player))
	}
	// 6. Make sure everyone else sees this player at the current position.
	c.broadcastEntityTeleport()
	return nil
}
