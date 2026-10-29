package server

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"minecraft-server/game"
	"minecraft-server/player"
	"minecraft-server/protocol"
	"minecraft-server/world"
)

// teamOps summarises the Update Teams packets in pkts as "team:mode[:members]".
func teamOps(t *testing.T, pkts []sentPacket) []string {
	t.Helper()
	var out []string
	for _, p := range pkts {
		if p.id != CbPlayUpdateTeams {
			continue
		}
		buf := bytes.NewBuffer(p.body)
		team, _ := protocol.ReadStringFromBuf(buf)
		mode, _ := buf.ReadByte()
		if !strings.HasPrefix(team, nameTagTeamPrefix) {
			continue // sidebar lines
		}
		op := fmt.Sprintf("%s:%d", team, mode)
		if mode == teamModeCreate {
			_, _ = protocol.ReadStringFromBuf(buf) // display name
			_, _ = buf.ReadByte()
			_, _ = protocol.ReadStringFromBuf(buf)
			_, _ = protocol.ReadStringFromBuf(buf)
			colour, _ := protocol.ReadVarInt(buf)
			prefix, _ := protocol.ReadStringFromBuf(buf)
			_, _ = protocol.ReadStringFromBuf(buf)
			op += fmt.Sprintf(":c%d:%s", colour, prefix)
		}
		if mode == teamModeCreate || mode == teamModeAddMembers || mode == teamModeRemoveMembers {
			n, _ := protocol.ReadVarInt(buf)
			var names []string
			for i := 0; i < int(n); i++ {
				s, _ := protocol.ReadStringFromBuf(buf)
				names = append(names, s)
			}
			op += ":" + strings.Join(names, ",")
		}
		out = append(out, op)
	}
	return out
}

func TestNameTags(t *testing.T) {
	s := New()
	inst := bareInstance(s, world.NewMemoryWorld())
	a := offlineConn(inst, "Alice", player.Survival, 0, 64, 0)
	b := offlineConn(inst, "Bob", player.Survival, 2, 64, 0)
	drainPackets(t, a)
	drainPackets(t, b)
	red := game.NameTag{Group: "red", Prefix: "§c● ", Colour: 'c'}

	playerBridge{conn: a}.SetNameTag(red)
	if got := teamOps(t, drainPackets(t, b)); len(got) != 1 || !strings.HasPrefix(got[0], "nt_red:0:c12:") || !strings.HasSuffix(got[0], ":Alice") {
		t.Fatalf("Bob sees %v, want nt_red created with Alice", got)
	}
	drainPackets(t, a)

	playerBridge{conn: b}.SetNameTag(red)
	if got := teamOps(t, drainPackets(t, a)); strings.Join(got, " ") != "nt_red:3:Bob" {
		t.Fatalf("Alice sees %v, want Bob added", got)
	}
	// Same tag again: nothing to send.
	playerBridge{conn: b}.SetNameTag(red)
	if got := teamOps(t, drainPackets(t, a)); len(got) != 0 {
		t.Fatalf("a repeat sent %v", got)
	}

	inst.LeaveAndAnnounce(b)
	if got := teamOps(t, drainPackets(t, a)); strings.Join(got, " ") != "nt_red:4:Bob" {
		t.Fatalf("Alice sees %v when Bob leaves, want Bob removed", got)
	}

	// Alice joins another instance: its groups replace these.
	other := bareInstance(s, world.NewMemoryWorld())
	drainPackets(t, a)
	inst.LeaveAndAnnounce(a)
	a.instance = other
	other.JoinAndAnnounce(a)
	if got := teamOps(t, drainPackets(t, a)); strings.Join(got, " ") != "nt_red:1" {
		t.Fatalf("Alice's client after the move: %v, want nt_red removed", got)
	}
}

// tagPrefixes maps each nt_ team the client was told about (create / update)
// to its latest prefix.
func tagPrefixes(t *testing.T, pkts []sentPacket, into map[string]string) {
	t.Helper()
	for _, p := range pkts {
		if p.id != CbPlayUpdateTeams {
			continue
		}
		team, mode, prefix := teamPrefixOf(t, p.body)
		if !strings.HasPrefix(team, nameTagTeamPrefix) {
			continue
		}
		switch mode {
		case teamModeCreate, teamModeUpdate:
			into[team] = prefix
		case teamModeRemove:
			delete(into, team)
		}
	}
}

// TestBotRoleInNameTag: a bot's name tag shows its role after the game's
// team dot, and follows it when the role changes.
func TestBotRoleInNameTag(t *testing.T) {
	inst, target := botWorld()
	guide := &fakeGuide{plan: game.BotPlan{Team: "red", Targets: []world.Position{target}}}
	b := addTestBot(t, inst, guide, DifficultyEasy, 0.5, 0.5)
	watcher := offlineConn(inst, "Watcher", player.Spectator, 0.5, 70, 0.5)
	inst.JoinAndAnnounce(watcher)
	drainPackets(t, watcher)
	playerBridge{conn: b}.SetNameTag(game.NameTag{Group: "bw_red", Prefix: "§c● ", Colour: 'c'})
	runTicks(inst, 20)
	seen := map[string]string{}
	tagPrefixes(t, drainPackets(t, watcher), seen)
	if seen["nt_bw_red_p"] != "§c● §6[Pusher] " {
		t.Fatalf("watcher's teams %q, want the bot as nt_bw_red_p with prefix %q", seen, "§c● §6[Pusher] ")
	}
	// No enemy bed left: a warrior now.
	guide.plan.Targets = nil
	runTicks(inst, 40)
	tagPrefixes(t, drainPackets(t, watcher), seen)
	if _, still := seen["nt_bw_red_p"]; still || seen["nt_bw_red_w"] != "§c● §c[Warrior] " {
		t.Fatalf("watcher's teams %q after the beds are gone, want only nt_bw_red_w", seen)
	}
}
