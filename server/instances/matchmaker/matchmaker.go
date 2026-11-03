// Package matchmaker holds the per-game waiting queues: who waits for which
// game, in join order, and when a queue has enough players to start a
// round. Starting it (an instance, moving players in) is the server's job.
package matchmaker

import (
	"fmt"
	"sync"
)

// Queues is a set of FIFO queues, one per game id, over any player type.
// Each player waits in at most one queue. Safe for concurrent use; the zero
// value is ready.
type Queues[M comparable] struct {
	mu     sync.Mutex
	queues map[string][]M // gameID → waiting players (FIFO)
	member map[M]string   // player → gameID it's currently queued for
}

// Join adds m to gameID's queue. Errors when m is already queued (for any
// game) or the queue is full (maxPlayers > 0 waiting already).
//
// When the queue then holds at least minPlayers (> 0), the first
// min(len, maxPlayers) players leave it and are returned: the round to
// start. Otherwise start is nil.
func (q *Queues[M]) Join(m M, gameID string, minPlayers, maxPlayers int) (start []M, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.queues == nil {
		q.queues = make(map[string][]M)
		q.member = make(map[M]string)
	}
	if existing, ok := q.member[m]; ok {
		return nil, fmt.Errorf("already queued for %s", existing)
	}
	queue := q.queues[gameID]
	if maxPlayers > 0 && len(queue) >= maxPlayers {
		return nil, fmt.Errorf("queue for %s is full", gameID)
	}
	queue = append(queue, m)
	q.queues[gameID] = queue
	q.member[m] = gameID

	if minPlayers <= 0 || len(queue) < minPlayers {
		return nil, nil
	}
	n := len(queue)
	if maxPlayers > 0 && n > maxPlayers {
		n = maxPlayers
	}
	start = make([]M, n)
	copy(start, queue[:n])
	q.queues[gameID] = queue[n:]
	for _, picked := range start {
		delete(q.member, picked)
	}
	return start, nil
}

// Leave takes m out of whatever queue it's in (no-op when it isn't).
func (q *Queues[M]) Leave(m M) {
	q.mu.Lock()
	defer q.mu.Unlock()
	gameID, ok := q.member[m]
	if !ok {
		return
	}
	delete(q.member, m)
	queue := q.queues[gameID]
	for i, qm := range queue {
		if qm == m {
			q.queues[gameID] = append(queue[:i], queue[i+1:]...)
			break
		}
	}
}

// Size is how many players wait for gameID.
func (q *Queues[M]) Size(gameID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queues[gameID])
}

// Of returns the game m waits for, or ("", false).
func (q *Queues[M]) Of(m M) (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	gameID, ok := q.member[m]
	return gameID, ok
}
