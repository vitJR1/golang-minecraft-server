package commands

import (
	"fmt"
	"minecraft-server/game"
	"minecraft-server/server"
	"minecraft-server/server/botai"
	"minecraft-server/templates"
	"strings"
)

// bot.go: /bot — AI players (server AddBot / RemoveBot / BotMatch).

func init() {
	server.RegisterCommand(&server.Command{
		Name:     "bot",
		NeedsOp:  true,
		Help:     "/bot add <team> [easy|normal|hard] | match <mode> [difficulty] | remove <name> | list | clear — AI players",
		Run:      cmdBot,
		Complete: completeBot,
	})
}

// cmdBot: /bot add <team> [easy|normal|hard] | remove <name> | list | clear
func cmdBot(c *server.ClientConnection, args []string) {
	usage := "Usage: /bot add <team> [easy|normal|hard]  |  /bot match <mode> [difficulty]  |  /bot remove <name>  |  /bot list  |  /bot clear"
	if len(args) == 0 {
		_ = c.SendMessage(usage)
		return
	}
	s := c.Server()
	switch strings.ToLower(args[0]) {
	case "add":
		if len(args) < 2 || len(args) > 3 {
			_ = c.SendMessage(usage)
			return
		}
		diff := botai.DifficultyNormal
		if len(args) == 3 {
			d, ok := botai.ParseDifficulty(args[2])
			if !ok {
				_ = c.SendMessage("Difficulty is easy, normal or hard")
				return
			}
			diff = d
		}
		b, err := s.AddBot(c.Instance(), args[1], diff)
		if err != nil {
			_ = c.SendMessage("Can't add a bot: " + err.Error())
			return
		}
		_ = c.SendMessage(fmt.Sprintf("Added %s (%s) to team %s", b.Name(), diff, args[1]))
	case "remove":
		if len(args) != 2 {
			_ = c.SendMessage(usage)
			return
		}
		b, _, ok := s.FindPlayer(args[1])
		if !ok || !b.IsBot() {
			_ = c.SendMessage("No bot named " + args[1])
			return
		}
		s.RemoveBot(b)
		_ = c.SendMessage("Removed " + b.Name())
	case "match":
		cmdBotMatch(c, args[1:])
	case "list":
		bots := s.Bots(nil)
		if len(bots) == 0 {
			_ = c.SendMessage("No bots.")
			return
		}
		for _, b := range bots {
			_ = c.SendMessage(fmt.Sprintf("%s — %s, in %s", b.Name(), b.Bot().Difficulty(), b.Instance().ID))
		}
	case "clear":
		bots := s.Bots(c.Instance())
		for _, b := range bots {
			s.RemoveBot(b)
		}
		_ = c.SendMessage(fmt.Sprintf("Removed %d bot(s)", len(bots)))
	default:
		_ = c.SendMessage(usage)
	}
}

// completeBot completes /bot: add <team> [difficulty] with the teams of
// the game in c's instance (game.BotTeams), remove <name> with the bots.
func completeBot(c *server.ClientConnection, args []string) []string {
	s, argIdx := c.Server(), len(args)
	if argIdx == 1 {
		return []string{"add", "match", "remove", "list", "clear"}
	}
	switch strings.ToLower(args[0]) {
	case "add":
		switch argIdx {
		case 2:
			return s.BotTeamNames(c.Instance())
		case 3:
			return []string{"easy", "normal", "hard"}
		}
	case "match":
		switch argIdx {
		case 2:
			return game.ArenaKinds()
		case 3:
			return []string{"easy", "normal", "hard"}
		case 4:
			return s.TemplateNames()
		}
	case "remove":
		if argIdx == 2 {
			var names []string
			for _, b := range s.Bots(nil) {
				names = append(names, b.Name())
			}
			return names
		}
	}
	return nil
}

// cmdBotMatch: /bot match <mode> [easy|normal|hard] [template].
func cmdBotMatch(c *server.ClientConnection, args []string) {
	if len(args) < 1 || len(args) > 3 {
		_ = c.SendMessage("Usage: /bot match <mode> [easy|normal|hard] [template] — modes: " +
			strings.Join(game.ArenaKinds(), ", "))
		return
	}
	diff := botai.DifficultyNormal
	if len(args) >= 2 {
		d, ok := botai.ParseDifficulty(args[1])
		if !ok {
			_ = c.SendMessage("botai.Difficulty is easy, normal or hard")
			return
		}
		diff = d
	}
	template := templates.BedwarsDotaMap
	if len(args) == 3 {
		template = args[2]
	}
	name, n, err := c.Server().BotMatch(c, strings.ToLower(args[0]), template, diff)
	if err != nil {
		_ = c.SendMessage("Bot match: " + err.Error())
		return
	}
	_ = c.SendMessage(fmt.Sprintf("§aBot match §f%s§a: %d %s bots. You're watching — /leave to stop.", name, n, diff))
}
