package server

import "minecraft-server/protocol"

// day_time.go: a fixed time of day per instance (BedWars: always noon).
//
// The vanilla client runs its own clock — the sun moves and night falls
// after ten minutes even when the server never says a word — and every
// Respawn into another dimension (each instance move and resync hops
// through the_end) gives it a fresh level at time 0 with the daylight
// cycle on. Update Time (Cb 0x5E: world age Long, time of day Long; a
// negative time of day turns the client's daylight cycle off and holds the
// sun at its absolute value) is therefore sent after every such Respawn:
// on joining an instance and in resyncView. An instance without a fixed
// time sends nothing: that same Respawn already set the client's clock
// running again.

// FixedTime is the instance's frozen time of day (ticks, 6000 = noon), if
// it has one.
func (i *Instance) FixedTime() (int64, bool) {
	v := i.fixedTime.Load()
	return v - 1, v != 0
}

// FreezeTime holds the instance's time of day at dayTime (0..23999; 6000 =
// noon) for everyone in it, now and on every join.
func (i *Instance) FreezeTime(dayTime int64) {
	dayTime = ((dayTime % 24000) + 24000) % 24000
	i.fixedTime.Store(dayTime + 1) // 0 means "not frozen"
	i.Players.Range(func(c *ClientConnection) { c.sendDayTime() })
}

// sendDayTime tells the client the time of day of its instance.
func (c *ClientConnection) sendDayTime() {
	inst := c.instance
	if inst == nil || c.player == nil {
		return
	}
	t, frozen := inst.FixedTime()
	if !frozen {
		return
	}
	// The client freezes on a negative value: noon is -6000; 0 can't be
	// negative, so it's nudged to 1.
	t = -max(t, 1)
	var payload []byte
	payload = append(payload, protocol.WriteLong(int64(inst.Tick()))...)
	payload = append(payload, protocol.WriteLong(t)...)
	_ = c.safeWrite(CbPlayUpdateTime, payload)
}
