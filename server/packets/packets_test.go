package packets

import (
	"bytes"
	"testing"

	"minecraft-server/protocol"
	"minecraft-server/world"
)

func cat(parts ...[]byte) *bytes.Buffer {
	return bytes.NewBuffer(bytes.Join(parts, nil))
}

func TestReadHandshake(t *testing.T) {
	h, err := ReadHandshake(cat(protocol.WriteVarInt32(763), protocol.WriteString("localhost"),
		[]byte{0x63, 0xDD}, protocol.WriteVarInt32(NextLogin)))
	if err != nil || h.Protocol != 763 || h.Address != "localhost" || h.Port != 25565 || h.Next != NextLogin {
		t.Errorf("handshake %+v %v", h, err)
	}
	if _, err := ReadHandshake(cat(protocol.WriteVarInt32(763))); err == nil {
		t.Error("short handshake must fail")
	}
}

func TestReadMoves(t *testing.T) {
	xyz := bytes.Join([][]byte{protocol.WriteDouble(1.5), protocol.WriteDouble(64), protocol.WriteDouble(-2)}, nil)
	look := bytes.Join([][]byte{protocol.WriteFloat(90), protocol.WriteFloat(-10)}, nil)
	m, err := ReadPos(cat(xyz, []byte{1}))
	if err != nil || !m.HasPos || m.HasLook || m.X != 1.5 || m.Z != -2 || !m.OnGround {
		t.Errorf("pos %+v %v", m, err)
	}
	m, err = ReadPosRot(cat(xyz, look, []byte{0}))
	if err != nil || !m.HasPos || !m.HasLook || m.Yaw != 90 || m.Pitch != -10 || m.OnGround {
		t.Errorf("pos+rot %+v %v", m, err)
	}
	m, err = ReadRot(cat(look, []byte{1}))
	if err != nil || m.HasPos || !m.HasLook || m.Yaw != 90 {
		t.Errorf("rot %+v %v", m, err)
	}
	if _, err := ReadPos(cat(xyz)); err == nil {
		t.Error("missing onGround must fail")
	}
}

func TestReadUseItemOn(t *testing.T) {
	u, err := ReadUseItemOn(cat(protocol.WriteVarInt32(0), protocol.WritePosition(3, 64, -7), protocol.WriteVarInt32(1),
		protocol.WriteFloat(0.5), protocol.WriteFloat(1), protocol.WriteFloat(0.25), []byte{0}, protocol.WriteVarInt32(42)))
	if err != nil || u.Pos != (world.Position{X: 3, Y: 64, Z: -7}) || u.Face != 1 || u.CursorY != 1 || u.CursorZ != 0.25 || u.Sequence != 42 {
		t.Errorf("use item on %+v %v", u, err)
	}
}

func TestReadPlayerActionAndInteract(t *testing.T) {
	a, err := ReadPlayerAction(cat(protocol.WriteVarInt32(ActionFinishDig), protocol.WritePosition(1, 2, 3), []byte{5}, protocol.WriteVarInt32(9)))
	if err != nil || a.Action != ActionFinishDig || a.Pos != (world.Position{X: 1, Y: 2, Z: 3}) || a.Face != 5 || a.Sequence != 9 {
		t.Errorf("player action %+v %v", a, err)
	}
	in, err := ReadInteract(cat(protocol.WriteVarInt32(77), protocol.WriteVarInt32(InteractAttack), []byte{0}))
	if err != nil || in.Target != 77 || in.Type != InteractAttack {
		t.Errorf("interact %+v %v", in, err)
	}
	pc, err := ReadPlayerCommand(cat(protocol.WriteVarInt32(5), protocol.WriteVarInt32(CommandStartSprint), protocol.WriteVarInt32(0)))
	if err != nil || pc.EntityID != 5 || pc.Action != CommandStartSprint {
		t.Errorf("player command %+v %v", pc, err)
	}
}

func TestReadClickAndHeld(t *testing.T) {
	buf := cat([]byte{0}, protocol.WriteVarInt32(3), []byte{0xFC, 0x19}, []byte{1}, protocol.WriteVarInt32(4), []byte{0xAB})
	k, err := ReadClick(buf)
	if err != nil || k.Window != 0 || k.Slot != -999 || k.Button != 1 || k.Mode != 4 {
		t.Errorf("click %+v %v", k, err)
	}
	if buf.Len() != 1 {
		t.Errorf("the changed slots must stay in the buffer, %d left", buf.Len())
	}
	slot, err := ReadHeldSlot(cat([]byte{0xFF, 0xFF}))
	if err != nil || slot != -1 {
		t.Errorf("held slot %d %v, want -1 (signed)", slot, err)
	}
}

func TestReadSuggestAndEncryption(t *testing.T) {
	r, err := ReadSuggestRequest(cat(protocol.WriteVarInt32(7), protocol.WriteString("/op _")))
	if err != nil || r.ID != 7 || r.Text != "/op _" {
		t.Errorf("suggest %+v %v", r, err)
	}
	e, err := ReadEncryptionResponse(cat(protocol.WriteVarInt32(2), []byte{1, 2}, protocol.WriteVarInt32(1), []byte{3}))
	if err != nil || !bytes.Equal(e.SharedSecret, []byte{1, 2}) || !bytes.Equal(e.VerifyToken, []byte{3}) {
		t.Errorf("encryption response %+v %v", e, err)
	}
}
