package lobby

import (
	"encoding/binary"
	"fmt"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

func TestLobbySetting(t *testing.T) {
	conn := dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule) VALUES
		('emu', 5, '1v1', 'one on one', '{"battle_gauge":1,"battle_time":90}'),
		('console', 6, '1v1', '', '')`); err != nil {
		t.Fatal(err)
	}
	n := 0
	enter := func(lobbyID uint16, side byte) *testClient {
		c := dialLobby(t, addr)
		m := NewClientNotice(0x9950)
		m.Writer().Write([]byte("emulator=pcsx2\nsync=00000000000000aa\n"))
		c.send(m)
		n++
		c.login(registerAccount(t), "******", fmt.Sprintf("pilot%d", n))
		if a := c.ask(0x6305, u16(lobbyID)); a.Status != StatusSuccess {
			t.Fatalf("EnterLobby status %x", a.Status)
		}
		c.ask(0x640E, []byte{side})
		return c
	}
	started := func(c *testClient) bool {
		t.Helper()
		c.ask(0x600E, nil)
		for _, m := range c.queue {
			if m.Command == 0x6910 {
				return true
			}
		}
		return false
	}
	explain := func(c *testClient, lobbyID uint16) string {
		r := c.ask(0x6308, u16(lobbyID)).Reader()
		r.Read16()
		return r.ReadString()
	}

	a6, b6 := enter(6, model.EntryAeug), enter(6, model.EntryTitans)
	if started(a6) || started(b6) {
		t.Fatal("1v1 started in an emu lobby whose setting is for console")
	}
	if s := explain(a6, 6); s != "<B>Lobby 06<END>" {
		t.Errorf("lobby 6 explain %q", s)
	}
	if s := explain(a6, 5); s != "<B>Lobby 05<BR>one on one<END>" {
		t.Errorf("lobby 5 explain %q", s)
	}

	a5, b5 := enter(5, model.EntryAeug), enter(5, model.EntryTitans)
	for i, c := range []*testClient{a5, b5} {
		c.expect(0x6910, CategoryNotice)
		rule := c.ask(0x6914, nil).Body
		gauge, time := binary.BigEndian.Uint16(rule[72:]), binary.BigEndian.Uint16(rule[80:])
		if gauge != 1 || time != 90 {
			t.Errorf("player %d: gauge %d time %d, want 1 90", i, gauge, time)
		}
	}
}
