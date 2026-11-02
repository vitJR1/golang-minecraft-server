package world

import "testing"

func TestNeighbor(t *testing.T) {
	p := Position{X: 1, Y: 2, Z: 3}
	want := map[int]Position{
		0: {1, 1, 3}, 1: {1, 3, 3}, 2: {1, 2, 2}, 3: {1, 2, 4}, 4: {0, 2, 3}, 5: {2, 2, 3}, 9: p,
	}
	for face, w := range want {
		if got := p.Neighbor(face); got != w {
			t.Errorf("Neighbor(%d) = %v, want %v", face, got, w)
		}
	}
}
