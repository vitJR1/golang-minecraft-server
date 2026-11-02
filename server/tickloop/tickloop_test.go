package tickloop

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestStepRunsHandlersInOrder(t *testing.T) {
	var l Loop
	var order []int
	var ticks []uint64
	l.Handle(func(tick uint64) { order = append(order, 1); ticks = append(ticks, tick) })
	l.Handle(func(uint64) { order = append(order, 2) })
	l.Step()
	l.Step()
	if len(order) != 4 || order[0] != 1 || order[1] != 2 || order[2] != 1 || order[3] != 2 {
		t.Errorf("order %v, want [1 2 1 2]", order)
	}
	if ticks[0] != 1 || ticks[1] != 2 || l.Count() != 2 {
		t.Errorf("ticks %v count %d, want [1 2] and 2", ticks, l.Count())
	}
}

func TestPanicDoesNotStopTheOthers(t *testing.T) {
	var l Loop
	ran := false
	l.Handle(func(uint64) { panic("boom") })
	l.Handle(func(uint64) { ran = true })
	l.Step()
	if !ran {
		t.Error("a panicking handler kept the next one from running")
	}
}

func TestHandleFromAHandler(t *testing.T) {
	var l Loop
	added := 0
	l.Handle(func(uint64) {
		if added == 0 {
			l.Handle(func(uint64) { added++ })
		}
	})
	l.Step() // the new handler joins from the next tick
	l.Step()
	if added != 1 {
		t.Errorf("added handler ran %d times, want 1", added)
	}
}

func TestAdvanceAndSet(t *testing.T) {
	var l Loop
	l.Set(100)
	if got := l.Advance(5); got != 105 || l.Count() != 105 {
		t.Errorf("Advance: %d / %d, want 105", got, l.Count())
	}
}

func TestRunAndStop(t *testing.T) {
	var l Loop
	var calls atomic.Int64
	l.Handle(func(uint64) { calls.Add(1) })
	done := make(chan struct{})
	go func() { l.Run(); close(done) }()
	time.Sleep(4 * Interval)
	before := 0
	l.Stop(func() { before++ })
	l.Stop(func() { before++ }) // second call is a no-op
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run didn't return after Stop")
	}
	if before != 1 {
		t.Errorf("before ran %d times, want once", before)
	}
	if calls.Load() == 0 {
		t.Error("Run never ticked")
	}
	n := l.Count()
	time.Sleep(2 * Interval)
	if l.Count() != n {
		t.Error("ticks after Stop")
	}
	if l.Max() < l.Last() {
		t.Errorf("max %v < last %v", l.Max(), l.Last())
	}
}
