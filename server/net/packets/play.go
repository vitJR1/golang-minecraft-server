package packets

import (
	"bytes"
	"fmt"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

// SuggestRequest is Command Suggestions Request: VarInt(transaction id) +
// String(the whole chat box).
type SuggestRequest struct {
	ID   int32
	Text string
}

// ReadSuggestRequest decodes a Command Suggestions Request.
func ReadSuggestRequest(buf *bytes.Buffer) (SuggestRequest, error) {
	id, err := protocol.ReadVarInt(buf)
	if err != nil {
		return SuggestRequest{}, fmt.Errorf("suggest req txID: %w", err)
	}
	text, err := protocol.ReadStringFromBuf(buf)
	if err != nil {
		return SuggestRequest{}, fmt.Errorf("suggest req text: %w", err)
	}
	return SuggestRequest{ID: int32(id), Text: text}, nil
}

// UseItemOn is Use Item On Block: hand(VarInt) + Position + face(VarInt) +
// cursor x/y/z (3×Float) + inside_block(Bool) + sequence(VarInt).
type UseItemOn struct {
	Pos                       world.Position
	Face                      int
	CursorX, CursorY, CursorZ float32 // where on the face, each axis in [0, 1]
	Sequence                  int32
}

// ReadUseItemOn decodes Use Item On Block. Hand, the cursor and inside_block
// are read leniently, as vanilla never sends them broken.
func ReadUseItemOn(buf *bytes.Buffer) (UseItemOn, error) {
	var u UseItemOn
	_, _ = protocol.ReadVarInt(buf) // hand
	x, y, z, err := protocol.ReadPosition(buf)
	if err != nil {
		return u, fmt.Errorf("use item on: position: %w", err)
	}
	u.Pos = world.Position{X: x, Y: y, Z: z}
	face, err := protocol.ReadVarInt(buf)
	if err != nil {
		return u, fmt.Errorf("use item on: face: %w", err)
	}
	u.Face = face
	u.CursorX, _ = protocol.ReadFloat(buf)
	u.CursorY, _ = protocol.ReadFloat(buf)
	u.CursorZ, _ = protocol.ReadFloat(buf)
	_, _ = protocol.ReadBool(buf) // inside_block
	seq, err := protocol.ReadVarInt(buf)
	if err != nil {
		return u, fmt.Errorf("use item on: sequence: %w", err)
	}
	u.Sequence = int32(seq)
	return u, nil
}

// Player Action statuses.
const (
	ActionStartDig  = 0
	ActionCancelDig = 1
	ActionFinishDig = 2
	ActionDropStack = 3 // ctrl+Q
	ActionDropItem  = 4 // Q
	ActionRelease   = 5 // shoot arrow / finish eating
	ActionSwapHands = 6
)

// PlayerAction is Player Action: status(VarInt) + Position + face(Byte) +
// sequence(VarInt).
type PlayerAction struct {
	Action   int
	Pos      world.Position
	Face     int
	Sequence int32
}

// ReadPlayerAction decodes Player Action.
func ReadPlayerAction(buf *bytes.Buffer) (PlayerAction, error) {
	var a PlayerAction
	action, err := protocol.ReadVarInt(buf)
	if err != nil {
		return a, fmt.Errorf("player action: %w", err)
	}
	a.Action = action
	x, y, z, err := protocol.ReadPosition(buf)
	if err != nil {
		return a, fmt.Errorf("player action: position: %w", err)
	}
	a.Pos = world.Position{X: x, Y: y, Z: z}
	face, _ := buf.ReadByte()
	a.Face = int(face)
	seq, err := protocol.ReadVarInt(buf)
	if err != nil {
		return a, fmt.Errorf("player action: sequence: %w", err)
	}
	a.Sequence = int32(seq)
	return a, nil
}

// Interact types.
const (
	InteractUse    = 0
	InteractAttack = 1
	InteractAt     = 2
)

// Interact is Interact's head: target entity id + type. The rest (target
// point, hand, sneaking) is left unread.
type Interact struct {
	Target int32
	Type   int
}

// ReadInteract decodes the head of Interact.
func ReadInteract(buf *bytes.Buffer) (Interact, error) {
	target, err := protocol.ReadVarInt(buf)
	if err != nil {
		return Interact{}, fmt.Errorf("interact target: %w", err)
	}
	typ, err := protocol.ReadVarInt(buf)
	if err != nil {
		return Interact{}, fmt.Errorf("interact type: %w", err)
	}
	return Interact{Target: int32(target), Type: typ}, nil
}

// Player Command actions the server tracks.
const (
	CommandStartSneak  = 0
	CommandStopSneak   = 1
	CommandStartSprint = 3
	CommandStopSprint  = 4
)

// PlayerCommand is Player Command: entity id + action (+ jump boost, read
// and dropped).
type PlayerCommand struct {
	EntityID int32
	Action   int
}

// ReadPlayerCommand decodes Player Command.
func ReadPlayerCommand(buf *bytes.Buffer) (PlayerCommand, error) {
	eid, err := protocol.ReadVarInt(buf)
	if err != nil {
		return PlayerCommand{}, fmt.Errorf("player command entity: %w", err)
	}
	action, err := protocol.ReadVarInt(buf)
	if err != nil {
		return PlayerCommand{}, fmt.Errorf("player command action: %w", err)
	}
	_, _ = protocol.ReadVarInt(buf) // jump boost
	return PlayerCommand{EntityID: int32(eid), Action: action}, nil
}

// ReadHeldSlot decodes Set Held Item: the new hotbar slot as a signed Short
// (vanilla sends 0..8; anything else is the handler's to refuse).
func ReadHeldSlot(buf *bytes.Buffer) (int16, error) {
	raw, err := protocol.ReadUShortFromBuf(buf)
	if err != nil {
		return 0, fmt.Errorf("set held item: %w", err)
	}
	return int16(raw), nil // signed cast preserves bits
}

// Click is Click Container's head: window id, slot, button, mode. The
// changed slots and carried item follow in buf for inventory.ReadClaim.
type Click struct {
	Window byte
	Slot   int16 // -999 = outside the window
	Button byte
	Mode   int
}

// ReadClick decodes the head of Click Container (the state id is dropped).
func ReadClick(buf *bytes.Buffer) (Click, error) {
	var c Click
	var err error
	if c.Window, err = buf.ReadByte(); err != nil {
		return c, fmt.Errorf("click container: window id: %w", err)
	}
	_, _ = protocol.ReadVarInt(buf) // state id
	slot, err := protocol.ReadUShortFromBuf(buf)
	if err != nil {
		return c, fmt.Errorf("click container: slot: %w", err)
	}
	// -999 arrives as 0xFC19; the int16 cast gives the sign back.
	c.Slot = int16(slot)
	c.Button, _ = buf.ReadByte()
	c.Mode, _ = protocol.ReadVarInt(buf)
	return c, nil
}
