package playerlist

import (
	"sort"
	"testing"
)

func TestList(t *testing.T) {
	var l List[string]
	l.Add(1, "Alice", [16]byte{1}, "a")
	l.Add(2, "Bob", [16]byte{2}, "b")
	if l.Count() != 2 {
		t.Fatalf("count %d", l.Count())
	}
	if m, ok := l.ByName("Bob"); !ok || m != "b" {
		t.Fatal("ByName")
	}
	if _, ok := l.ByName("bob"); ok {
		t.Fatal("names are exact")
	}
	if m, ok := l.Get(1); !ok || m != "a" {
		t.Fatal("Get")
	}
	l.Add(1, "Alice", [16]byte{1}, "a2") // same id replaces
	got := l.Snapshot()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "a2" {
		t.Fatalf("snapshot %v", got)
	}
	if len(l.UUIDs()) != 2 {
		t.Fatal("UUIDs")
	}
	l.Remove(2)
	n := 0
	l.Range(func(string) { n++ })
	if n != 1 {
		t.Fatalf("range visited %d", n)
	}
}
