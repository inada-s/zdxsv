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
	if _, err := conn.Exec(`INSERT INTO m_rule (id, battle_count, visible_team, battle_time, battle_gauge, reload_type, damage_level, awake_type, awake_inc_ratio, awake_dec_ratio)
		VALUES ('tai', 0, 0, 90, 1, 0, 3, 0, 100, 100)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES
		('emu', 5, '1v1', 'one on one', 'tai', ''),
		('emu', 7, '1v1', '', 'missing', ''),
		('emu', 4, '', 'no widescreen', '', 'none'),
		('emu', 8, '', '', '', 'widescreen'),
		('console', 6, '1v1', '', 'tai', '')`); err != nil {
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
	battleRule := func(lobbyID uint16, gauge, time uint16) {
		t.Helper()
		a, b := enter(lobbyID, model.EntryAeug), enter(lobbyID, model.EntryTitans)
		for i, c := range []*testClient{a, b} {
			c.expect(0x6910, CategoryNotice)
			rule := c.ask(0x6914, nil).Body
			g, tm := binary.BigEndian.Uint16(rule[72:]), binary.BigEndian.Uint16(rule[80:])
			if g != gauge || tm != time {
				t.Errorf("lobby %d player %d: gauge %d time %d, want %d %d", lobbyID, i, g, tm, gauge, time)
			}
		}
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
	if s := explain(a6, 4); s != "<B>Lobby 04<BR>no widescreen<END>" {
		t.Errorf("lobby 4 with hack none: explain %q", s)
	}
	if b := hackPayload(t, a6, 8); le32(b, len(b)-4) != 0x00c22cc0+0x68 {
		t.Errorf("lobby 8 with hack widescreen: return address %08x", le32(b, len(b)-4))
	}
	hackPayload(t, a6, 3)

	battleRule(5, 1, 90)
	battleRule(7, 600, 210)
}
