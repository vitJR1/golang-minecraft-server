package server

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// Janitor: game instances (arenas, matchmaker rounds) are created at
// runtime and normally removed by their game's EndGame — but an arena
// nobody ever joined, or a round every player disconnected from, has no
// one left to trigger that. Each such instance still burns a 20 Hz tick
// loop and holds its world in memory. The janitor sweeps ephemeral
// instances that have been empty for emptyInstanceTTL and removes them.

// Vars (not consts) so tests can shrink them.
var (
	// emptyInstanceTTL is how long an ephemeral instance may sit empty
	// before the janitor removes it. Generous enough that "everyone
	// briefly disconnected" doesn't nuke a match in progress.
	emptyInstanceTTL = 5 * time.Minute

	// janitorInterval is the sweep period.
	janitorInterval = 30 * time.Second
)

// startJanitor launches the sweep loop. Called from New; stopped via
// stopJanitor (Shutdown does this).
func (s *Server) startJanitor() {
	go func() {
		ticker := time.NewTicker(janitorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.janitorStop:
				return
			case <-ticker.C:
				s.janitorSweep(time.Now())
			}
		}
	}()
}

// stopJanitor terminates the sweep loop. Idempotent.
func (s *Server) stopJanitor() {
	s.janitorStopOnce.Do(func() { close(s.janitorStop) })
}

// janitorSweep removes every ephemeral instance that has been empty since
// before now-emptyInstanceTTL. Emptiness is tracked lazily: the first sweep
// that sees an empty instance stamps emptySince; a later sweep still seeing
// it empty past the TTL removes it; any sweep seeing players clears the
// stamp. RemoveInstance re-checks the player count under the registry lock,
// so a join racing the final sweep loses at most the arena (they get an
// error), never the player.
func (s *Server) janitorSweep(now time.Time) {
	s.mu.RLock()
	instances := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		instances = append(instances, i)
	}
	s.mu.RUnlock()

	for _, inst := range instances {
		if !inst.Ephemeral {
			continue
		}
		if inst.Players.Count() > 0 {
			inst.emptySince.Store(0)
			continue
		}
		since := inst.emptySince.Load()
		if since == 0 {
			inst.emptySince.Store(now.UnixNano())
			continue
		}
		if now.Sub(time.Unix(0, since)) < emptyInstanceTTL {
			continue
		}
		if err := s.RemoveInstance(inst.ID); err != nil {
			// Someone joined between our count check and the removal —
			// clear the stamp and let the next sweep re-evaluate.
			inst.emptySince.Store(0)
			continue
		}
		slog.Info("janitor: removed empty instance", "instance", inst.ID)
	}
}

// markEphemeral flags an instance as janitor-collectable and stamps it
// empty-as-of-now, so an arena nobody ever joins is collected one TTL
// after creation.
func markEphemeral(inst *Instance, now time.Time) {
	inst.Ephemeral = true
	inst.emptySince.Store(now.UnixNano())
}

// ephemeralState is embedded in Instance (kept here so the janitor's
// fields live next to the code that owns them).
type ephemeralState struct {
	// Ephemeral marks instances the janitor may remove when empty:
	// runtime-created arenas and matchmaker rounds. Hub, auth, and lobby
	// instances stay false. Set before the instance takes traffic.
	Ephemeral bool

	// emptySince is the UnixNano timestamp of the sweep that first saw
	// this instance empty (0 = had players at the last sweep, or not
	// ephemeral). Written by the janitor and markEphemeral only.
	emptySince atomic.Int64
}
