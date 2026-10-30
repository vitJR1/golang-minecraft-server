package server

// Wire values the sidebar / name-tag tests check the packets against
// (minecraft-data 1.20), kept apart from server/scoreboard's own so the tests
// pin the protocol rather than echo the implementation.
const (
	nameTagTeamPrefix = "nt_"
	sidebarObjective  = "sidebar"
	sidebarMaxLines   = 15 // the client shows at most 15

	objectiveUpdate byte = 2

	teamModeCreate        byte = 0
	teamModeRemove        byte = 1
	teamModeUpdate        byte = 2
	teamModeAddMembers    byte = 3
	teamModeRemoveMembers byte = 4
)
