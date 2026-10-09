package server

import (
	"log/slog"
	"sync"
	"time"
)

// shutdownKickTimeout bounds how long Shutdown waits for the disconnect
// packets to flush. Each cleanup already waits ≤1s for its writer; this is
// the overall cap so a wedged connection can't stall the whole restart.
const shutdownKickTimeout = 5 * time.Second

// Shutdown gracefully winds the server down: every player gets a Play
// Disconnect with reason (so the client shows a message instead of
// "Connection lost"), every connection is cleaned up, and every instance's
// tick loop is stopped. Closing the TCP listener and the storage backends
// is the caller's job (main owns those). Safe to call once; a second call
// finds everything already closed and is a no-op in effect.
func (s *Server) Shutdown(reason string) {
	s.mu.RLock()
	instances := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		instances = append(instances, i)
	}
	s.mu.RUnlock()

	// Kick concurrently — sequential cleanup would pay each connection's
	// writer-flush wait (≤1s) one after another.
	var wg sync.WaitGroup
	for _, inst := range instances {
		for _, c := range inst.Players.snapshot() {
			wg.Add(1)
			go func(c *ClientConnection) {
				defer wg.Done()
				_ = c.sendPlayDisconnect(reason)
				c.cleanup()
			}(c)
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(shutdownKickTimeout):
		slog.Warn("shutdown: kick timeout, proceeding", "timeout", shutdownKickTimeout)
	}

	for _, inst := range instances {
		inst.Stop()
	}
	s.stopJanitor()
	slog.Info("server shut down", "kicked_instances", len(instances))
}
