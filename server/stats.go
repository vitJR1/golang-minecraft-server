package server

import (
	"runtime"
	"time"

	"minecraft-server/server/metrics"
)

// Stats is the /stats snapshot (server/metrics): players per instance, the
// connection counters, tick timings, goroutines and heap.
func (s *Server) Stats() metrics.Stats {
	s.mu.RLock()
	instances := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		instances = append(instances, i)
	}
	s.mu.RUnlock()

	perInstance := make(map[string]int, len(instances))
	players := 0
	var worstTick, lastTick int64
	for _, i := range instances {
		n := i.Players.Count()
		perInstance[i.ID] = n
		players += n
		worstTick = max(worstTick, int64(i.ticks.Max()))
		lastTick = max(lastTick, int64(i.ticks.Last()))
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return metrics.Stats{
		UptimeSeconds:  int64(time.Since(s.startTime).Seconds()),
		Players:        players,
		Instances:      perInstance,
		ConnsRejected:  s.connsRejected.Load(),
		QueueFullKicks: s.queueFullKicks.Load(),
		WorstTickMs:    float64(worstTick) / 1e6,
		LastTickMs:     float64(lastTick) / 1e6,
		Goroutines:     runtime.NumGoroutine(),
		HeapAllocMB:    float64(mem.HeapAlloc) / (1 << 20),
	}
}
