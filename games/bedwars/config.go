package bedwars

import "time"

// Tunable knobs for the 4-team layout and round pacing. Everything that a
// future "расширение" might want to vary lives here, not scattered through
// the logic — Single Responsibility for "what the numbers are".
const (
	// baseY is the top surface Y of every island platform (feet stand at
	// baseY+1). Matches the templateBaseY the rest of the server uses.
	baseY = 64

	// islandRadius gives a (2*islandRadius+1)² platform per team.
	islandRadius = 4

	// islandDistance is how far each island sits from world origin along
	// its cardinal axis. Big enough that islands don't touch.
	islandDistance = 24

	// centerRadius is the half-size of the small central island.
	centerRadius = 3

	// voidY: a player whose feet drop below this counts as having fallen
	// into the void → death. The engine has no fall/void detection of its
	// own, so OnTick polls for it (see bedWars.checkVoid).
	voidY = 40

	// voidScanInterval throttles the void poll: once every N ticks (20/s).
	voidScanInterval = 5

	// endDelay is how long the win banner stays up before the instance is
	// torn down and everyone is sent back to the hub.
	endDelay = 5 * time.Second
)

// spectatorPos is where eliminated players are parked (high above center,
// in Spectator gamemode) so they can watch the rest of the round.
var spectatorPos = struct{ X, Y, Z float64 }{0, baseY + 20, 0}
