package matchmaker

import (
	"slices"
	"testing"
)

func TestJoinHoldsBelowMin(t *testing.T) {
	var q Queues[string]
	start, err := q.Join("alice", "bw", 2, 4)
	if err != nil || start != nil {
		t.Fatalf("Join: %v %v, want held", start, err)
	}
	if q.Size("bw") != 1 {
		t.Errorf("size %d, want 1", q.Size("bw"))
	}
	if g, ok := q.Of("alice"); !ok || g != "bw" {
		t.Errorf("Of = %q %v", g, ok)
	}
}

func TestJoinStartsAtMinInOrder(t *testing.T) {
	var q Queues[string]
	_, _ = q.Join("alice", "bw", 2, 4)
	start, err := q.Join("bob", "bw", 2, 4)
	if err != nil || !slices.Equal(start, []string{"alice", "bob"}) {
		t.Fatalf("start = %v %v, want [alice bob]", start, err)
	}
	if q.Size("bw") != 0 {
		t.Errorf("picked players should leave the queue, size %d", q.Size("bw"))
	}
	if _, ok := q.Of("alice"); ok {
		t.Error("a picked player is no longer queued")
	}
	if _, err := q.Join("alice", "bw", 2, 4); err != nil {
		t.Errorf("a picked player may queue again: %v", err)
	}
}

func TestJoinRejects(t *testing.T) {
	var q Queues[string]
	_, _ = q.Join("alice", "bw", 0, 2) // min 0: never starts
	if _, err := q.Join("alice", "ffa", 0, 2); err == nil {
		t.Error("double queue should be rejected")
	}
	_, _ = q.Join("bob", "bw", 0, 2)
	if _, err := q.Join("carol", "bw", 0, 2); err == nil {
		t.Error("full queue should be rejected")
	}
}

func TestLeave(t *testing.T) {
	var q Queues[string]
	_, _ = q.Join("alice", "bw", 3, 4)
	_, _ = q.Join("bob", "bw", 3, 4)
	q.Leave("alice")
	q.Leave("nobody")
	if q.Size("bw") != 1 {
		t.Errorf("size %d, want 1", q.Size("bw"))
	}
	if _, ok := q.Of("alice"); ok {
		t.Error("alice left")
	}
	start, _ := q.Join("carol", "bw", 2, 4)
	if !slices.Equal(start, []string{"bob", "carol"}) {
		t.Errorf("start = %v, want [bob carol]", start)
	}
}
