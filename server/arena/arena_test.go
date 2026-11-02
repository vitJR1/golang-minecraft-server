package arena

import (
	"slices"
	"testing"
)

func TestRegistry(t *testing.T) {
	var r Registry
	r.Add("bw-2", "bedwars")
	r.Add("bw-1", "bedwars")
	r.Add("bw-1x1-3", "bedwars-1x1")
	if !r.Is("bw-1") || r.Is("hub") {
		t.Error("Is")
	}
	if got := r.OfKind("bedwars"); !slices.Equal(got, []string{"bw-1", "bw-2"}) {
		t.Errorf("OfKind(bedwars) = %v", got)
	}
	if got := r.OfKind(""); len(got) != 3 {
		t.Errorf("OfKind(\"\") = %v, want all three", got)
	}
	r.Remove("bw-1")
	r.Remove("nope")
	if r.Is("bw-1") {
		t.Error("Remove")
	}
}

func TestNextNameSkipsTaken(t *testing.T) {
	var r Registry
	taken := map[string]bool{"bw-1": true, "bw-2": true}
	if got := r.NextName("bedwars", func(n string) bool { return taken[n] }); got != "bw-3" {
		t.Errorf("NextName = %q, want bw-3", got)
	}
	if got := r.NextName("bedwars-1x1", func(string) bool { return false }); got != "bw-1x1-4" {
		t.Errorf("NextName = %q, want bw-1x1-4 (one serial for every kind)", got)
	}
	if got := r.NextName("ffa", func(string) bool { return false }); got != "ffa-5" {
		t.Errorf("NextName = %q, want ffa-5", got)
	}
}
