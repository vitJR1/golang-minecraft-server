package commands

import (
	"fmt"
	"minecraft-server/server"
	"strings"
)

// spectate.go: /spectate — watching is the server's Spectate.

func init() {
	server.RegisterCommand(&server.Command{
		Name:     "spectate",
		Aliases:  []string{"watch"},
		Help:     "/spectate [arena] — watch a running game in spectator mode",
		Run:      cmdSpectate,
		Complete: completeSpectate,
	})
}

// cmdSpectate: /spectate [arena] — watch a running arena; no argument lists
// them.
func cmdSpectate(c *server.ClientConnection, args []string) {
	s := c.Server()
	if len(args) == 0 {
		arenas := s.ArenasOfKind("")
		if len(arenas) == 0 {
			_ = c.SendMessage("No running arenas. Ops can start a bot match: /bot match <mode>")
			return
		}
		_ = c.SendMessage("Usage: /spectate <arena> — running: " + strings.Join(arenas, ", "))
		return
	}
	inst := s.GetInstance(args[0])
	if inst == nil || !s.IsArena(args[0]) {
		_ = c.SendMessage("No arena named " + args[0])
		return
	}
	if err := s.Spectate(c, inst); err != nil {
		_ = c.SendMessage("Can't watch: " + err.Error())
		return
	}
	_ = c.SendMessage(fmt.Sprintf("§7Watching §f%s§7. /leave to stop.", inst.ID))
}
