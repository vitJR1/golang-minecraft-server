package server

import (
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/features/command"
)

// sendDeclareCommands sends the command tree (command.Tree) of the commands
// c may run.
func (c *ClientConnection) sendDeclareCommands() error {
	cmds := commandsVisibleTo(c)
	names := make([]string, len(cmds))
	for i, cmd := range cmds {
		names[i] = cmd.Name
	}
	return c.safeWrite(packetid.CbPlayDeclareCommands, command.Tree(names))
}

// commandsVisibleTo returns the subset of registered commands that c is
// allowed to see — op-only ones are hidden from non-ops so they don't
// appear in /<TAB> autocomplete or in the brigadier tree the client
// uses for syntax highlighting. Mirror the gate in RunCommand: if the
// command needs op and the player isn't op, drop it.
func commandsVisibleTo(c *ClientConnection) []*Command {
	if c == nil || c.server == nil {
		return commands.All()
	}
	return commands.VisibleTo(c)
}
