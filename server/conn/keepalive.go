package conn

import (
	"sync/atomic"
	"time"
)

// Keep-alive timing. Atomics so tests can shorten them while connections'
// keep-alive goroutines read them. Defaults match vanilla: 20s between
// sends, 30s ceiling before kicking a non-responding client.
var (
	keepAliveIntervalNanos atomic.Int64
	keepAliveTimeoutNanos  atomic.Int64
)

func init() {
	keepAliveIntervalNanos.Store(int64(20 * time.Second))
	keepAliveTimeoutNanos.Store(int64(30 * time.Second))
}

// KeepAliveInterval returns the current send cadence.
func KeepAliveInterval() time.Duration { return time.Duration(keepAliveIntervalNanos.Load()) }

// SetKeepAliveInterval changes the send cadence; it takes effect on the next
// tick of every running keep-alive loop.
func SetKeepAliveInterval(d time.Duration) { keepAliveIntervalNanos.Store(int64(d)) }

// KeepAliveTimeout returns the current ack ceiling.
func KeepAliveTimeout() time.Duration { return time.Duration(keepAliveTimeoutNanos.Load()) }

// SetKeepAliveTimeout changes the ack ceiling; it takes effect immediately
// for any client already waiting on a response.
func SetKeepAliveTimeout(d time.Duration) { keepAliveTimeoutNanos.Store(int64(d)) }

// KeepAlive is the Cb KeepAlive in flight: its id (0: none) and when it was
// sent. Written by the keep-alive loop, answered from the read loop.
type KeepAlive struct {
	pendingID atomic.Int64
	sentNanos atomic.Int64
}

// Next decides at now: send a new keep-alive (id ≠ 0; it is marked pending
// before it goes out, so a fast client can't answer first), wait for the
// outstanding one (id 0), or give up on it (timedOut, after age).
func (k *KeepAlive) Next(now time.Time) (id int64, timedOut bool, age time.Duration) {
	if prev := k.pendingID.Load(); prev != 0 {
		age = now.Sub(time.Unix(0, k.sentNanos.Load()))
		return 0, age > KeepAliveTimeout(), age
	}
	id = now.UnixNano()
	k.sentNanos.Store(id)
	k.pendingID.Store(id)
	return id, false, 0
}

// Pending is the outstanding id (0: none).
func (k *KeepAlive) Pending() int64 { return k.pendingID.Load() }

// Answer clears the outstanding keep-alive if id is it. False: not the
// outstanding id — the server never re-sends (a late reply means a timeout
// kick), so that's a forged echo; the real outstanding id stays put.
func (k *KeepAlive) Answer(id int64) bool { return k.pendingID.CompareAndSwap(id, 0) }

// Clear forgets the outstanding keep-alive.
func (k *KeepAlive) Clear() { k.pendingID.Store(0) }
