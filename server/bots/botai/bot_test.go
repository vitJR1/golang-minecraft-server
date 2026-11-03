package botai

import (
	"math/rand"
	"testing"
)

// TestBotReaction: swings wait a human 30–100 ms — in ticks 0..2, about
// 1.3 on average.
func TestBotReaction(t *testing.T) {
	b := &Bot{rng: rand.New(rand.NewSource(1))}
	sum := 0.0
	const n = 20000
	for k := 0; k < n; k++ {
		r := b.reactTicks()
		if r > 2 {
			t.Fatalf("reaction of %d ticks (> 100 ms)", r)
		}
		sum += float64(r)
	}
	if mean := sum / n * 50; mean < 60 || mean > 70 {
		t.Fatalf("mean reaction %.1f ms, want ≈ 65", mean)
	}
}
