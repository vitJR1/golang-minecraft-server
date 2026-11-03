// Package scoreboard is the client's scoreboard as the server uses it: the
// per-player sidebar and the name-tag groups, each kept as the state a
// client shows and turned into the Update Objectives / Display Objective /
// Update Score / Update Teams packets that change one state into another.
// Packet ids anchor on minecraft-data 1.20.
//
// The client keeps its scoreboard across the server's two-hop Respawn and
// kicks on an objective or team created twice, which is why the server
// keeps each connection's state and sends only differences.
package scoreboard

import (
	"bytes"

	"minecraft-server/protocol"
	"minecraft-server/protocol/packetid"
)

// Packet is one scoreboard packet to send.
type Packet struct {
	ID      int32
	Payload []byte
}

// colorReset is the Update Teams "formatting" value for no colour.
const colorReset = 21

// Update Objectives (0x58) actions.
const (
	objectiveCreate byte = 0
	objectiveRemove byte = 1
	objectiveUpdate byte = 2
)

// objectivePayload builds Update Objectives: name, action, and for
// create/update the display text (Chat) + type 0 (integer).
func objectivePayload(name string, action byte, title string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(name))
	buf.WriteByte(action)
	if action != objectiveRemove {
		buf.Write(protocol.TextComponent(title))
		protocol.WriteVarInt32ToBuffer(&buf, 0) // type: integer
	}
	return buf.Bytes()
}

// displayObjectivePayload builds Display Objective (0x51): slot + name.
func displayObjectivePayload(slot byte, name string) []byte {
	var buf bytes.Buffer
	buf.WriteByte(slot)
	buf.Write(protocol.WriteString(name))
	return buf.Bytes()
}

// scorePayload builds Update Score (0x5B) action 0: holder, objective, value.
func scorePayload(holder, objective string, value int32) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(holder))
	protocol.WriteVarInt32ToBuffer(&buf, 0) // create / update
	buf.Write(protocol.WriteString(objective))
	protocol.WriteVarInt32ToBuffer(&buf, value)
	return buf.Bytes()
}

// scoreRemovePayload builds Update Score action 1: holder, objective.
func scoreRemovePayload(holder, objective string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(holder))
	protocol.WriteVarInt32ToBuffer(&buf, 1) // remove
	buf.Write(protocol.WriteString(objective))
	return buf.Bytes()
}

// Update Teams (0x5A) modes (minecraft-data 1.20: 3/4 carry a "players"
// array).
const (
	teamModeCreate        byte = 0
	teamModeRemove        byte = 1
	teamModeUpdate        byte = 2
	teamModeAddMembers    byte = 3
	teamModeRemoveMembers byte = 4
)

// teamPayload builds Update Teams mode 0 (with members) or 2: display name,
// friendly flags, name-tag visibility, collision rule, colour, prefix,
// suffix.
func teamPayload(team string, mode byte, colour int32, prefix string, members []string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(team))
	buf.WriteByte(mode)
	buf.Write(protocol.TextComponent(team))
	buf.WriteByte(0) // friendly flags
	buf.Write(protocol.WriteString("always"))
	buf.Write(protocol.WriteString("always"))
	protocol.WriteVarInt32ToBuffer(&buf, colour)
	buf.Write(protocol.TextComponent(prefix))
	buf.Write(protocol.TextComponent(""))
	if mode == teamModeCreate {
		protocol.WriteVarInt32ToBuffer(&buf, int32(len(members)))
		for _, m := range members {
			buf.Write(protocol.WriteString(m))
		}
	}
	return buf.Bytes()
}

// teamMembersPayload builds Update Teams mode 3 / 4.
func teamMembersPayload(team string, mode byte, members []string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(team))
	buf.WriteByte(mode)
	protocol.WriteVarInt32ToBuffer(&buf, int32(len(members)))
	for _, m := range members {
		buf.Write(protocol.WriteString(m))
	}
	return buf.Bytes()
}

// teamRemovePayload builds Update Teams mode 1.
func teamRemovePayload(team string) []byte {
	var buf bytes.Buffer
	buf.Write(protocol.WriteString(team))
	buf.WriteByte(teamModeRemove)
	return buf.Bytes()
}

func teams(payload []byte) Packet { return Packet{packetid.CbPlayUpdateTeams, payload} }
