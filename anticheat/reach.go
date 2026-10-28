package anticheat

import "math"

// reach.go: the geometry of interaction-range checks, as vanilla 1.20.1
// measures it — from the eyes to a block's centre or an entity's hitbox.

// Vanilla limits.
const (
	// EyeHeight / SneakEyeHeight are the player's eye levels above the feet.
	EyeHeight      = 1.62
	SneakEyeHeight = 1.27

	// PlayerWidth / PlayerHeight / SneakHeight are the player hitbox.
	PlayerWidth  = 0.6
	PlayerHeight = 1.8
	SneakHeight  = 1.5

	// MaxDigReach: ServerPlayerGameMode refuses a dig whose block centre is
	// farther than 6 blocks from the eyes.
	MaxDigReach = 6.0
	// MaxUseReach: Use Item On Block is honoured within 8 blocks of the
	// feet (distanceToSqr(centre) < 64).
	MaxUseReach = 8.0
	// MaxEntityReach: Interact is honoured while the eyes are within 6
	// blocks of the target's hitbox.
	MaxEntityReach = 6.0

	// AttackReach / CreativeAttackReach are what the client itself allows
	// (GameRenderer.pick): 3 blocks to the hitbox in survival, 6 in
	// creative. A hit beyond them didn't come from a vanilla client.
	AttackReach         = 3.0
	CreativeAttackReach = 6.0
	// HitboxMargin absorbs interpolation and float error on the victim box.
	HitboxMargin = 0.1

	// InterpolationTicks: other players are drawn lerping toward their last
	// position over 3 ticks, so the client may show a position that old.
	InterpolationTicks = 3
	// UnconfirmedWindow is how far back to look for a victim's positions
	// when the attacker has answered no Ping yet (1 s).
	UnconfirmedWindow = 20
)

// Vec is a point in world space.
type Vec struct{ X, Y, Z float64 }

// Box is an axis-aligned box.
type Box struct{ Min, Max Vec }

// Eye returns the eye position of a player standing at feet.
func Eye(feet Vec, sneaking bool) Vec {
	h := EyeHeight
	if sneaking {
		h = SneakEyeHeight
	}
	return Vec{feet.X, feet.Y + h, feet.Z}
}

// Dist is the Euclidean distance between two points.
func Dist(a, b Vec) float64 {
	return math.Sqrt((a.X-b.X)*(a.X-b.X) + (a.Y-b.Y)*(a.Y-b.Y) + (a.Z-b.Z)*(a.Z-b.Z))
}

// BlockCentre is the centre of the block at integer coordinates.
func BlockCentre(x, y, z int) Vec {
	return Vec{float64(x) + 0.5, float64(y) + 0.5, float64(z) + 0.5}
}

// FeetBox is a hitbox width wide and height tall standing at feet.
func FeetBox(feet Vec, width, height float64) Box {
	w := width / 2
	return Box{Vec{feet.X - w, feet.Y, feet.Z - w}, Vec{feet.X + w, feet.Y + height, feet.Z + w}}
}

// PlayerBox is the hitbox of a player sample.
func PlayerBox(s Sample) Box {
	h := PlayerHeight
	if s.Sneaking {
		h = SneakHeight
	}
	return FeetBox(Vec{s.X, s.Y, s.Z}, PlayerWidth, h)
}

// Expand grows the box by d on every side.
func (b Box) Expand(d float64) Box {
	return Box{Vec{b.Min.X - d, b.Min.Y - d, b.Min.Z - d}, Vec{b.Max.X + d, b.Max.Y + d, b.Max.Z + d}}
}

// DistanceTo is the distance from p to the nearest point of the box (0
// inside).
func (b Box) DistanceTo(p Vec) float64 {
	clamp := func(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
	return Dist(p, Vec{clamp(p.X, b.Min.X, b.Max.X), clamp(p.Y, b.Min.Y, b.Max.Y), clamp(p.Z, b.Min.Z, b.Max.Z)})
}

// MinReach is the shortest distance from eye to any of the victim's
// candidate hitboxes (expanded by HitboxMargin): the most charitable reading
// of where the attacker could have seen the victim. ok is false without
// samples.
func MinReach(eye Vec, samples []Sample) (dist float64, ok bool) {
	dist = math.Inf(1)
	for _, s := range samples {
		dist = math.Min(dist, PlayerBox(s).Expand(HitboxMargin).DistanceTo(eye))
	}
	return dist, len(samples) > 0
}

// SeenFrom is the first tick of the victim's history an attacker may have
// been looking at: InterpolationTicks before the newest tick the attacker
// confirmed with a Pong, or UnconfirmedWindow ticks back without one.
func SeenFrom(now, confirmed uint64, hasConfirmed bool) uint64 {
	back := uint64(UnconfirmedWindow)
	from := now
	if hasConfirmed && confirmed <= now {
		from = confirmed
		back = InterpolationTicks
	}
	if from < back {
		return 0
	}
	return from - back
}
