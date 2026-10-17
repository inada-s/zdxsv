package lobby

import (
	"encoding/binary"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	"zdxsv/pkg/lobby/message"

	"golang.org/x/text/encoding/japanese"
)

func sjis(t *testing.T, s string) string {
	t.Helper()
	b, err := japanese.ShiftJIS.NewEncoder().String(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// chat sends a chat line and returns the next replies SERVER chat notices.
func (c *testClient) chat(line string, replies int) []string {
	c.t.Helper()
	n := message.NewClientNotice(0x6701)
	n.Writer().Write(encryptString(n.Seq, sjis(c.t, line)))
	c.send(n)
	return c.serverChat(replies)
}

// serverChat returns the next n chat notices from SERVER, skipping other
// chat notices (the lobby echo of a line).
func (c *testClient) serverChat(n int) []string {
	c.t.Helper()
	var got []string
	for len(got) < n {
		r := c.expect(0x6702, message.CategoryNotice).Reader()
		if r.ReadString() != "SERVER" {
			continue
		}
		r.ReadString()
		got = append(got, r.ReadString())
	}
	return got
}

// Lobby 3 and 4 explain answers overflow the client's buffer: the body has a
// fixed size, ends with the return address the client then jumps to, and
// carries R5900 code from offset 32 on.
const (
	hackBodySize = 0x0120 - 8
	hackCodeAt   = 32
)

func hackPayload(t *testing.T, c *testClient, lobbyID uint16) []byte {
	t.Helper()
	body := c.ask(0x6308, u16(lobbyID)).Body
	if len(body) != hackBodySize {
		t.Fatalf("lobby %d explain: %d bytes, want %d", lobbyID, len(body), hackBodySize)
	}
	if id, size := binary.BigEndian.Uint16(body), binary.BigEndian.Uint16(body[2:]); id != lobbyID || int(size) != hackBodySize-4 {
		t.Fatalf("lobby %d explain header: id %d size %d", lobbyID, id, size)
	}
	return body
}

func le32(b []byte, off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }

func TestLobbyExplainHacks(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())
	ac := registerAccount(t)
	c := dialLobby(t, addr)
	userID, _, _ := c.login(ac, "******", "pilot")

	// Lobby 3: pad delay table fill, entered through the client's own return path.
	pad := hackPayload(t, c, 3)
	if ret := le32(pad, len(pad)-4); ret != 0x00c22cc0 {
		t.Errorf("pad delay return address %08x", ret)
	}
	if op := le32(pad, hackCodeAt); op != 0x27bdffb0 {
		t.Errorf("pad delay first op %08x", op)
	}
	// li a0, 2; li a1, 5: every table entry gets initial 2, limits 5.
	if a0, a1 := le32(pad, hackCodeAt+5*4), le32(pad, hackCodeAt+6*4); a0 != 0x24040002 || a1 != 0x24050005 {
		t.Errorf("pad delay values %08x %08x", a0, a1)
	}

	// Lobby 4: widescreen patch; the 9th op is the first value it patches in.
	const wideOn, wideOff = 0x24843f40, 0x24843f80
	wide := func() uint32 {
		t.Helper()
		b := hackPayload(t, c, 4)
		if ret := le32(b, len(b)-4); ret != 0x00c22cc0+0x68 {
			t.Errorf("widescreen return address %08x", ret)
		}
		return le32(b, hackCodeAt+8*4)
	}
	system := func() uint32 {
		t.Helper()
		u, err := db.DefaultDB.GetUser(userID)
		if err != nil {
			t.Fatal(err)
		}
		return u.System
	}
	if v := wide(); v != wideOff {
		t.Errorf("new user widescreen value %08x, want off", v)
	}

	// The w0 / w1 chat commands work in lobby 1 only; its entry count lists them.
	// An ask after a chat line orders the checks after its handler.
	if a := c.ask(0x6305, u16(2)); a.Status != message.StatusSuccess {
		t.Fatalf("EnterLobby status %x", a.Status)
	}
	c.chat("ｗ１", 0)
	c.ask(0x6408, nil)
	if v, s := wide(), system(); v != wideOff || s != 0 {
		t.Errorf("w1 outside lobby 1: widescreen value %08x, system %x", v, s)
	}

	c.ask(0x6305, u16(1))
	c.ask(0x640F, u16(1))
	if got := c.serverChat(5); got[3] != sjis(t, "ｗ０：ワイドスクリーンパッチ無効化") {
		t.Errorf("lobby 1 notices %q", got)
	}

	if got := c.chat("ｗ１", 3); got[0] != sjis(t, "ワイドスクリーンパッチを有効化") {
		t.Errorf("w1 reply %q", got)
	}
	if v, s := wide(), system(); v != wideOn || s != 1 {
		t.Errorf("after w1: widescreen value %08x, system %x", v, s)
	}

	if got := c.chat("ｗ０", 3); got[0] != sjis(t, "ワイドスクリーンパッチを無効化") {
		t.Errorf("w0 reply %q", got)
	}
	if v, s := wide(), system(); v != wideOff || s != 0 {
		t.Errorf("after w0: widescreen value %08x, system %x", v, s)
	}
}
