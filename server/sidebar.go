package server

import (
	"sync"

	"minecraft-server/server/scoreboard"
)

// sidebar.go: the per-player scoreboard sidebar (the panel on the right) —
// the state and its packets are server/scoreboard; the connection keeps
// what its client shows (c.sidebar, under sidebarMu) so only changes go
// out. The client's scoreboard survives our two-hop Respawn, so nothing is
// re-sent on resync; MovePlayer clears the sidebar before the next instance
// may set one.

// setSidebar shows (or updates) the sidebar with title and lines, top
// first; only what changed goes to the client. More than
// scoreboard.SidebarMaxLines lines are cut.
func (c *ClientConnection) setSidebar(title string, lines []string) {
	if c.player == nil {
		return
	}
	c.sidebarMu.Lock()
	defer c.sidebarMu.Unlock()
	next, pkts := scoreboard.SetSidebar(c.sidebar, title, lines)
	c.writeScoreboard(pkts)
	c.sidebar = next
}

// clearSidebar takes the sidebar down (no-op when none is shown).
func (c *ClientConnection) clearSidebar() {
	c.sidebarMu.Lock()
	defer c.sidebarMu.Unlock()
	prev := c.sidebar
	if prev == nil {
		return
	}
	c.sidebar = nil
	if c.player == nil {
		return
	}
	c.writeScoreboard(scoreboard.ClearSidebar(prev))
}

// writeScoreboard sends scoreboard packets in order.
func (c *ClientConnection) writeScoreboard(pkts []scoreboard.Packet) {
	for _, p := range pkts {
		_ = c.safeWrite(p.ID, p.Payload)
	}
}

// sidebarMutex is embedded in ClientConnection; kept as a named type so
// the zero value is ready to use.
type sidebarMutex = sync.Mutex
