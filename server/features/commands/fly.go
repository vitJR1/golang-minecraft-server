package commands

import (
	"fmt"
	"minecraft-server/server"
)

// fly.go: /fly — the flight permission itself is the server's (fly.go there).

func init() {
	server.RegisterCommand(&server.Command{
		Name:    "fly",
		NeedsOp: true,
		Help:    "/fly [player] — toggle flight (until the next gamemode change or instance move)",
		Run:     cmdFly,
	})
}

// cmdFly: /fly [player] — toggle flight.
func cmdFly(c *server.ClientConnection, args []string) {
	target := c
	if len(args) > 1 {
		_ = c.SendMessage("Usage: /fly [player]")
		return
	}
	if len(args) == 1 {
		conn, _, ok := c.Server().FindPlayer(args[0])
		if !ok {
			_ = c.SendMessage("Player not found: " + args[0])
			return
		}
		target = conn
	}
	on := !target.CanFly()
	target.SetFlight(on)
	state := "off"
	if on {
		state = "on"
	}
	_ = c.SendMessage(fmt.Sprintf("Flight %s for %s", state, target.Name()))
	if target != c {
		_ = target.SendMessage("Flight " + state)
	}
}
