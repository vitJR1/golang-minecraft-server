package server

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"minecraft-server/anticheat"
	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/protocol"
)

// bot.go: server-side players driven by an AI instead of a client (/bot).
// A bot is a ClientConnection without a socket — its outbound queue is
// drained into the void — so to the rest of the server, the games and the
// other players it is a player like any other: it shows in the tab list,
// fights, takes damage and knockback, dies, picks up items, holds a kit.
// Its body moves through server-side physics (bot_physics.go), finds its
// way with bridging (bot_path.go) and acts through the brain (bot_brain.go),
// guided by the game (game.BotGuide).

// botNames are the names bots draw from, as BOT_<name> (16 characters max,
// letters/digits/underscore — a valid username, so commands can target it).
var botNames = []string{
	"Steve", "Alex", "Angel", "Bob", "Lina", "Max", "Nina", "Oscar",
	"Mia", "Leo", "Zoe", "Kai", "Ivy", "Rex", "Luna", "Finn",
	"Ruby", "Theo", "Ella", "Jack", "Nora", "Owen", "Iris", "Hugo",
	"Cleo", "Milo", "Ada", "Otto", "Vera", "Felix", "Gwen", "Ivan",
}

// botPrefix starts every bot's name.
const botPrefix = "BOT_"

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

// bot is the AI half of a bot connection.
type bot struct {
	c     *ClientConnection
	diff  Difficulty
	skill botSkill
	style botStyle
	rng   *rand.Rand

	phys physState

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

// pushVelocity queues a velocity the server gave the bot (giveVelocity).
func (b *bot) pushVelocity(vx, vy, vz float64) {
	b.pushMu.Lock()
	b.pending = &anticheat.Vec{X: vx, Y: vy, Z: vz}
	b.pushMu.Unlock()
}

// takePush returns and clears the queued push.
func (b *bot) takePush() (anticheat.Vec, bool) {
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
func (b *bot) reactTicks() uint64 {
	ms := botReactMinMs + b.rng.Intn(botReactMaxMs-botReactMinMs+1)
	t := float64(ms) / 50
	n := uint64(t)
	if b.rng.Float64() < t-float64(n) {
		n++
	}
	return n
}

// isBot reports whether c is a bot.
func (c *ClientConnection) isBot() bool { return c != nil && c.bot != nil }

// freeBotName picks an unused BOT_<name>, falling back to numbered names
// when all of them are online.
func (s *Server) freeBotName(rng *rand.Rand) string {
	order := rng.Perm(len(botNames))
	for round := 0; ; round++ {
		for _, i := range order {
			name := botPrefix + botNames[i]
			if round > 0 {
				name = fmt.Sprintf("%s%d", name, round+1)
			}
			if _, _, taken := s.FindPlayer(name); !taken {
				return name
			}
		}
	}
}

// AddBot creates a bot in inst, which must run a game with a bot guide, on
// the named team. The bot joins like a logged-in player (tab list, spawn,
// the game's join hook), then the guide places it on the team.
func (s *Server) AddBot(inst *Instance, team string, diff Difficulty) (*ClientConnection, error) {
	if inst == nil || inst.botGuide == nil {
		return nil, errors.New("this instance runs no game that bots can play")
	}
	rng := rand.New(rand.NewSource(rand.Int63()))
	name := s.freeBotName(rng)
	uuidBytes, err := protocol.WriteUUID(protocol.OfflineUUID(name))
	if err != nil {
		return nil, fmt.Errorf("bot uuid: %w", err)
	}
	var uuid [16]byte
	copy(uuid[:], uuidBytes)

	c := &ClientConnection{
		server:               s,
		state:                StatePlay,
		playerName:           name,
		compressionThreshold: protocol.CompressionDisabled,
		outbound:             make(chan outboundMsg, outboundQueueSize),
		done:                 make(chan struct{}),
		writerDone:           make(chan struct{}),
	}
	c.authed.Store(true)
	c.player = player.New(s.nextEntityID.Add(1), name, uuid)
	c.bot = &bot{c: c, diff: diff, skill: botSkills[diff], style: rollStyle(rng), rng: rng}
	c.bot.roleShown.Store(-1)
	go func() { // the "client": nothing reads what the server sends
		for range c.outbound {
		}
		close(c.writerDone)
	}()

	sp := inst.SpawnPoint
	c.player.MoveTo(sp.X, sp.Y, sp.Z, false)
	c.instance = inst
	inst.JoinAndAnnounce(c)
	c.bot.phys.Pos = c.feet()

	if err := inst.botGuide.BotJoin(inst.pluginCtx(), playerBridge{conn: c}, team); err != nil {
		c.cleanup()
		return nil, err
	}
	c.bot.ready.Store(true)
	slog.Info("bot added", "bot", name, "instance", inst.ID, "team", team, "difficulty", diff)
	return c, nil
}

// RemoveBot takes a bot out of the game and the server.
func (s *Server) RemoveBot(c *ClientConnection) {
	if !c.isBot() {
		return
	}
	slog.Info("bot removed", "bot", c.playerName)
	c.cleanup()
}

// botsIn lists the bots in inst (all instances when inst is nil), by name.
func (s *Server) botsIn(inst *Instance) []*ClientConnection {
	var out []*ClientConnection
	s.forEachPlayer(func(c *ClientConnection) {
		if c.isBot() && (inst == nil || c.instance == inst) {
			out = append(out, c)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].playerName < out[j].playerName })
	return out
}

// botSuggestions completes /bot: add <team> [difficulty] with the teams of
// the game in c's instance (game.BotTeams), remove <name> with the bots.
func (s *Server) botSuggestions(c *ClientConnection, parts []string, argIdx int) []string {
	if argIdx == 1 {
		return []string{"add", "match", "remove", "list", "clear"}
	}
	if len(parts) < 2 {
		return nil
	}
	switch strings.ToLower(parts[1]) {
	case "add":
		switch argIdx {
		case 2:
			return s.botTeamNames(c.instance)
		case 3:
			return []string{"easy", "normal", "hard"}
		}
	case "match":
		switch argIdx {
		case 2:
			return botMatchKinds()
		case 3:
			return []string{"easy", "normal", "hard"}
		case 4:
			return s.TemplateNames()
		}
	case "remove":
		if argIdx == 2 {
			var names []string
			for _, b := range s.botsIn(nil) {
				names = append(names, b.playerName)
			}
			return names
		}
	}
	return nil
}

// botTeamNames are the teams of the game in inst (game.BotTeams), if it
// names them.
func (s *Server) botTeamNames(inst *Instance) []string {
	if inst == nil {
		return nil
	}
	if bt, ok := inst.botGuide.(game.BotTeams); ok {
		return bt.BotTeams(inst.pluginCtx())
	}
	return nil
}

// botMatchKinds are the arena kinds /bot match offers.
func botMatchKinds() []string { return game.ArenaKinds() }

// cmdBot: /bot add <team> [easy|normal|hard] | remove <name> | list | clear
func cmdBot(c *ClientConnection, args []string) {
	usage := "Usage: /bot add <team> [easy|normal|hard]  |  /bot match <mode> [difficulty]  |  /bot remove <name>  |  /bot list  |  /bot clear"
	if len(args) == 0 {
		_ = c.sendSystemMessage(usage)
		return
	}
	s := c.server
	switch strings.ToLower(args[0]) {
	case "add":
		if len(args) < 2 || len(args) > 3 {
			_ = c.sendSystemMessage(usage)
			return
		}
		diff := DifficultyNormal
		if len(args) == 3 {
			d, ok := ParseDifficulty(args[2])
			if !ok {
				_ = c.sendSystemMessage("Difficulty is easy, normal or hard")
				return
			}
			diff = d
		}
		b, err := s.AddBot(c.instance, args[1], diff)
		if err != nil {
			_ = c.sendSystemMessage("Can't add a bot: " + err.Error())
			return
		}
		_ = c.sendSystemMessage(fmt.Sprintf("Added %s (%s) to team %s", b.playerName, diff, args[1]))
	case "remove":
		if len(args) != 2 {
			_ = c.sendSystemMessage(usage)
			return
		}
		b, _, ok := s.FindPlayer(args[1])
		if !ok || !b.isBot() {
			_ = c.sendSystemMessage("No bot named " + args[1])
			return
		}
		s.RemoveBot(b)
		_ = c.sendSystemMessage("Removed " + b.playerName)
	case "match":
		cmdBotMatch(c, args[1:])
	case "list":
		bots := s.botsIn(nil)
		if len(bots) == 0 {
			_ = c.sendSystemMessage("No bots.")
			return
		}
		for _, b := range bots {
			_ = c.sendSystemMessage(fmt.Sprintf("%s — %s, in %s", b.playerName, b.bot.diff, b.instance.ID))
		}
	case "clear":
		bots := s.botsIn(c.instance)
		for _, b := range bots {
			s.RemoveBot(b)
		}
		_ = c.sendSystemMessage(fmt.Sprintf("Removed %d bot(s)", len(bots)))
	default:
		_ = c.sendSystemMessage(usage)
	}
}
