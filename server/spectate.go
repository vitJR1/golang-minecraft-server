package server

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"minecraft-server/player"
	"minecraft-server/templates"
)

// spectate.go: watching a game without playing it. /spectate <arena> puts
// the caller into the arena's instance in spectator mode — flying, through
// walls, unseen by the players — and keeps the game out of it: the game's
// join and leave hooks don't fire for a watcher, so it gets no team, no
// kit, doesn't count for the lobby and can't end the round. It still sees
// what everyone there sees (the sidebar the game shows its players, titles,
// chat). Leaving — /leave, /hub, the game sending its players to a lobby —
// ends the watching like any instance move.
//
// /bot match <mode> [difficulty] [template] (op) is the shortcut for
// watching bots: a new arena of that mode, every team filled with bots, the
// round started, the caller watching.

// watchState is a connection's watching: the instance it's moving in to
// watch (target, during the move) and the one it watches (watched).
type watchState struct {
	target  atomic.Pointer[Instance]
	watched atomic.Pointer[Instance]
}

// watching reports whether c only watches i: the game's hooks skip it.
func (c *ClientConnection) watching(i *Instance) bool {
	return i != nil && (c.watch.target.Load() == i || c.watch.watched.Load() == i)
}

// Spectate moves c into inst as a watcher, in spectator mode, above the
// instance's spawn.
func (s *Server) Spectate(c *ClientConnection, inst *Instance) error {
	if c.isBot() {
		return errors.New("bots don't watch")
	}
	if c.instance == inst {
		if c.watching(inst) {
			return nil
		}
		return errors.New("you're playing in this game — /leave first")
	}
	c.watch.target.Store(inst)
	defer c.watch.target.Store(nil)
	c.player.SetGamemode(player.Spectator) // the Respawn of the move carries it
	sp := inst.SpawnPoint
	if err := s.MovePlayer(c, inst, sp.X, sp.Y+4, sp.Z); err != nil {
		return err
	}
	c.watch.watched.Store(inst)
	_ = c.sendGameModeChange(player.Spectator)
	return nil
}

// endWatch forgets watching old once c has left it.
func (c *ClientConnection) endWatch(old *Instance) {
	c.watch.watched.CompareAndSwap(old, nil)
}

// cmdSpectate: /spectate [arena] — watch a running arena; no argument lists
// them.
func cmdSpectate(c *ClientConnection, args []string) {
	s := c.server
	if len(args) == 0 {
		arenas := s.ArenasOfKind("")
		if len(arenas) == 0 {
			_ = c.sendSystemMessage("No running arenas. Ops can start a bot match: /bot match <mode>")
			return
		}
		_ = c.sendSystemMessage("Usage: /spectate <arena> — running: " + strings.Join(arenas, ", "))
		return
	}
	inst := s.GetInstance(args[0])
	if inst == nil || !s.IsArena(args[0]) {
		_ = c.sendSystemMessage("No arena named " + args[0])
		return
	}
	if err := s.Spectate(c, inst); err != nil {
		_ = c.sendSystemMessage("Can't watch: " + err.Error())
		return
	}
	_ = c.sendSystemMessage(fmt.Sprintf("§7Watching §f%s§7. /leave to stop.", inst.ID))
}

// botMatchFill is the most bots /bot match puts on one team.
const botMatchFill = 8

// botMatch creates an arena of kind from template, fills every team with
// bots of diff, starts the round and has c watch. Returns the arena name
// and the number of bots.
func (s *Server) botMatch(c *ClientConnection, kind, template string, diff Difficulty) (string, int, error) {
	name, err := s.CreateArena(kind, template, "")
	if err != nil {
		return "", 0, err
	}
	inst := s.GetInstance(name)
	if inst == nil || inst.botGuide == nil {
		return name, 0, fmt.Errorf("%s runs no game that bots can play", kind)
	}
	teams := s.botTeamNames(inst)
	if len(teams) == 0 {
		return name, 0, fmt.Errorf("%s doesn't name its teams", kind)
	}
	// Watch first, so the bots' joins reach the watcher's client.
	if err := s.Spectate(c, inst); err != nil {
		return name, 0, err
	}
	added := 0
	for _, team := range teams {
		for k := 0; k < botMatchFill; k++ {
			if _, err := s.AddBot(inst, team, diff); err != nil {
				break // the team is full
			}
			added++
		}
	}
	if inst.OnForceStart != nil {
		if err := inst.OnForceStart(); err != nil {
			return name, added, fmt.Errorf("start: %w", err)
		}
	}
	return name, added, nil
}

// cmdBotMatch: /bot match <mode> [easy|normal|hard] [template].
func cmdBotMatch(c *ClientConnection, args []string) {
	if len(args) < 1 || len(args) > 3 {
		_ = c.sendSystemMessage("Usage: /bot match <mode> [easy|normal|hard] [template] — modes: " +
			strings.Join(botMatchKinds(), ", "))
		return
	}
	diff := DifficultyNormal
	if len(args) >= 2 {
		d, ok := ParseDifficulty(args[1])
		if !ok {
			_ = c.sendSystemMessage("Difficulty is easy, normal or hard")
			return
		}
		diff = d
	}
	template := templates.BedwarsDotaMap
	if len(args) == 3 {
		template = args[2]
	}
	name, n, err := c.server.botMatch(c, strings.ToLower(args[0]), template, diff)
	if err != nil {
		_ = c.sendSystemMessage("Bot match: " + err.Error())
		return
	}
	_ = c.sendSystemMessage(fmt.Sprintf("§aBot match §f%s§a: %d %s bots. You're watching — /leave to stop.", name, n, diff))
}
