// Package botai is the mind of a bot (/bot): it decides each tick what the
// bot does — fight, shop, collect, bridge to an enemy bed and dig it out, play
// its part in the team — and drives its body. Physics and pathfinding are
// botnav's; the body, the world and the game are reached through Body, World
// and Mob (the server implements them over a socketless ClientConnection), and
// what counts as an enemy, where to shop and what to break come from the game
// (game.BotGuide). In order, each tick (brain.go):
//
//  1. Fight the nearest enemy within the difficulty's aggro range.
//  2. Fall back home on low health; run from its own lit TNT.
//  3. Shop when short of blocks or able to pay for wanted gear (tactics.go).
//  4. Save up / collect at the station, raid diamond and emerald piles.
//  5. Go for the first target (an enemy bed) and dig it out.
//
// A team's bots share a teamBrain (team.go) handing out roles: the pusher
// (pusher.go), defenders, an archer, attackers. Falls teach a team where to
// widen its bridges (reinforce.go).
package botai

import (
	"math"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"

	"minecraft-server/anticheat"
	"minecraft-server/server/botnav"
	"minecraft-server/world"
)

// Difficulty is how well a bot plays.
type Difficulty int

const (
	DifficultyEasy Difficulty = iota
	DifficultyNormal
	DifficultyHard
)

func (d Difficulty) String() string {
	switch d {
	case DifficultyEasy:
		return "easy"
	case DifficultyHard:
		return "hard"
	}
	return "normal"
}

// ParseDifficulty reads easy / normal / hard.
func ParseDifficulty(s string) (Difficulty, bool) {
	switch strings.ToLower(s) {
	case "easy", "e":
		return DifficultyEasy, true
	case "normal", "n", "":
		return DifficultyNormal, true
	case "hard", "h":
		return DifficultyHard, true
	}
	return 0, false
}

// botSkill is a difficulty's tuning.
type botSkill struct {
	reaction  uint64  // ticks from spotting an enemy to the first swing
	cps       float64 // clicks per second
	hitChance float64 // how often a swing in reach connects
	aggro     float64 // blocks within which enemies are engaged
	placeGap  uint64  // ticks between bridge blocks
	retreat   bool    // falls back home on low health
	crits     bool    // jumps into hits for critical damage
	maxLeap   int     // widest gap it sprint-jumps (0 = never)
	chaseCap  int     // most blocks it builds with to reach an enemy
	aimNoise  float64 // bow aim error, degrees
	farm      float64 // chance to stay at the forge for gear it can't afford yet
	hop       bool    // bunny-hops down straight stretches (sprint-jumps)
}

var botSkills = map[Difficulty]botSkill{
	DifficultyEasy: {reaction: 12, cps: 4, hitChance: 0.55, aggro: 8, placeGap: 8,
		chaseCap: 8, aimNoise: 4, farm: 0.3},
	DifficultyNormal: {reaction: 7, cps: 7, hitChance: 0.75, aggro: 12, placeGap: 5, retreat: true,
		maxLeap: 2, chaseCap: 24, aimNoise: 2, farm: 0.5, hop: true},
	DifficultyHard: {reaction: 3, cps: 10, hitChance: 0.9, aggro: 16, placeGap: 3, retreat: true, crits: true,
		maxLeap: 3, chaseCap: 48, aimNoise: 0.8, farm: 0.7, hop: true},
}

// botStyle is a bot's own taste, rolled when it joins: two bots of one
// difficulty don't play alike.
type botStyle struct {
	likesBow   bool    // buys a bow and arrows
	prefersTNT bool    // blasts hard bed defences rather than buying a tool
	armorFirst bool    // armour before a better sword
	leapCost   float64 // how much it dislikes leaps (path cost)
	greedy     bool    // raids any diamond / emerald pile, not just big ones
}

func rollStyle(rng *rand.Rand) botStyle {
	return botStyle{
		likesBow:   rng.Float64() < 0.4,
		prefersTNT: rng.Float64() < 0.5,
		armorFirst: rng.Float64() < 0.5,
		leapCost:   rng.Float64() * 3,
		greedy:     rng.Float64() < 0.6,
	}
}

// Bot is the AI half of a bot connection: it thinks each tick and moves its
// Body (the server's side of the connection).
type Bot struct {
	body  Body
	self  Mob
	diff  Difficulty
	skill botSkill
	style botStyle
	rng   *rand.Rand

	phys botnav.Body

	// pushes from other goroutines (knockback dealt on an attacker's read
	// loop, explosions) wait here for the bot's tick.
	pushMu  sync.Mutex
	pending *anticheat.Vec

	brain brainState

	// roleShown is the role in the bot's name tag (-1: none yet); atomic,
	// as name tags are built on other goroutines than the bot's tick.
	roleShown atomic.Int32

	// ready is set once AddBot is done: the bot is in the player list (so
	// the tick sees it) before the game has put it on a team and kitted it,
	// and it mustn't act meanwhile.
	ready atomic.Bool
}

// New makes the brain of a bot of difficulty diff acting through body, its
// style rolled from rng (which it keeps). It isn't Ready yet.
func New(body Body, diff Difficulty, rng *rand.Rand) *Bot {
	b := &Bot{body: body, self: body.Self(), diff: diff, skill: botSkills[diff], style: rollStyle(rng), rng: rng}
	b.roleShown.Store(-1)
	return b
}

// Difficulty is how well the bot plays.
func (b *Bot) Difficulty() Difficulty { return b.diff }

// SetReady lets the bot act (the game has placed it); Ready reports it.
func (b *Bot) SetReady()   { b.ready.Store(true) }
func (b *Bot) Ready() bool { return b.ready.Load() }

// Place puts the bot's body at feet (where it joined).
func (b *Bot) Place(feet anticheat.Vec) { b.phys.Pos = feet }

// FeetCell is the cell the bot's feet are in.
func (b *Bot) FeetCell() world.Position { return b.feetCell() }

func (b *Bot) feetCell() world.Position {
	p := b.phys.Pos
	return botnav.FeetCell(p.X, p.Y, p.Z)
}

// world is the bot's instance (nil-safe callers check Body.World first).
func (b *Bot) world() World { return b.body.World() }

// Push queues a velocity the server gave the bot (knockback, explosions:
// giveVelocity); its physics applies it on the next tick. Safe from any
// goroutine.
func (b *Bot) Push(vx, vy, vz float64) {
	b.pushMu.Lock()
	b.pending = &anticheat.Vec{X: vx, Y: vy, Z: vz}
	b.pushMu.Unlock()
}

// takePush returns and clears the queued push.
func (b *Bot) takePush() (anticheat.Vec, bool) {
	b.pushMu.Lock()
	defer b.pushMu.Unlock()
	if b.pending == nil {
		return anticheat.Vec{}, false
	}
	v := *b.pending
	b.pending = nil
	return v, true
}

// Reaction time added to a bot's swings: a human's 30–100 ms.
const (
	botReactMinMs = 30
	botReactMaxMs = 100
)

// reactTicks is a reaction of botReactMinMs..botReactMaxMs in ticks (50 ms
// each), rounded up or down at random so the delay averages out to the
// milliseconds drawn.
func (b *Bot) reactTicks() uint64 {
	ms := botReactMinMs + b.rng.Intn(botReactMaxMs-botReactMinMs+1)
	t := float64(ms) / 50
	n := uint64(t)
	if b.rng.Float64() < t-float64(n) {
		n++
	}
	return n
}

func floorF(v float64) int { return int(math.Floor(v)) }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
