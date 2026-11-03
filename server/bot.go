package server

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/server/bots/botai"
	"minecraft-server/server/net/conn"
)

// bot.go: server-side players driven by an AI instead of a client (/bot).
// A bot is a ClientConnection without a socket — its outbound queue is
// drained into the void — so to the rest of the server, the games and the
// other players it is a player like any other: it shows in the tab list,
// fights, takes damage and knockback, dies, picks up items, holds a kit.
// Its mind is a botai.Bot (server/bots/botai), which moves it with botnav's
// physics and pathfinding and acts through botBody (bot_body.go), guided by
// the game (game.BotGuide).

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
func (s *Server) AddBot(inst *Instance, team string, diff botai.Difficulty) (*ClientConnection, error) {
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

	c := &ClientConnection{server: s, state: StatePlay, playerName: name}
	c.link = conn.New(nil, nil)
	c.link.Discard() // the "client": nothing reads what the server sends
	c.authed.Store(true)
	c.player = player.New(s.nextEntityID.Add(1), name, uuid)
	c.bot = botai.New(botBody{c}, diff, rng)

	sp := inst.SpawnPoint
	c.player.MoveTo(sp.X, sp.Y, sp.Z, false)
	c.instance = inst
	inst.JoinAndAnnounce(c)
	c.bot.Place(c.feet())

	if err := inst.botGuide.BotJoin(inst.pluginCtx(), handle(c), team); err != nil {
		c.cleanup()
		return nil, err
	}
	c.bot.SetReady()
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
func (s *Server) Bots(inst *Instance) []*ClientConnection {
	var out []*ClientConnection
	s.forEachPlayer(func(c *ClientConnection) {
		if c.isBot() && (inst == nil || c.instance == inst) {
			out = append(out, c)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].playerName < out[j].playerName })
	return out
}

// BotTeamNames are the teams of the game in inst (game.BotTeams), if it
// names them.
func (s *Server) BotTeamNames(inst *Instance) []string {
	if inst == nil {
		return nil
	}
	if bt, ok := inst.botGuide.(game.BotTeams); ok {
		return bt.BotTeams(inst.pluginCtx())
	}
	return nil
}
