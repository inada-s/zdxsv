package main

import (
	"encoding/binary"
	"fmt"
	"testing"

	"golang.org/x/text/encoding/japanese"
)

func TestLobbySetting(t *testing.T) {
	conn := UseTestDB(t)
	addr := startLobby(t, NewMcs())
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
		a, b := enter(lobbyID, EntryAeug), enter(lobbyID, EntryTitans)
		for i, c := range []*testClient{a, b} {
			c.expect(0x6910, CategoryNotice)
			rule := c.ask(0x6914, nil).Body
			g, tm := binary.BigEndian.Uint16(rule[72:]), binary.BigEndian.Uint16(rule[80:])
			if g != gauge || tm != time {
				t.Errorf("lobby %d player %d: gauge %d time %d, want %d %d", lobbyID, i, g, tm, gauge, time)
			}
		}
	}

	a6, b6 := enter(6, EntryAeug), enter(6, EntryTitans)
	if started(a6) || started(b6) {
		t.Fatal("1v1 started in an emu lobby whose setting is for console")
	}
	if s := explain(a6, 6); s != "<B>Lobby 06<END>" {
		t.Errorf("lobby 6 explain %q", s)
	}
	if s := explain(a6, 5); s != "<B>Lobby 05<B><BR><B>one on one<END>" {
		t.Errorf("lobby 5 explain %q", s)
	}
	if s := explain(a6, 4); s != "<B>Lobby 04<B><BR><B>no widescreen<END>" {
		t.Errorf("lobby 4 with hack none: explain %q", s)
	}
	if s := explain(a6, 1); s != "<B>Lobby 01<END>" {
		t.Errorf("lobby 1 without a hack: explain %q", s)
	}
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('emu', 1, '', '', '', 'zproxy_test,chat_command')`); err != nil {
		t.Fatal(err)
	}
	if s := explain(a6, 1); s != "<B>Lobby 01<END>" {
		t.Errorf("zproxy_test lobby without an explain: %q", s)
	}
	if _, err := conn.Exec(`UPDATE m_lobby_setting SET explain = 'connection test' WHERE platform = 'emu' AND no = 1`); err != nil {
		t.Fatal(err)
	}
	if s := explain(a6, 1); s != "<B>Lobby 01<B><BR><B>connection test<END>" {
		t.Errorf("zproxy_test lobby with an explain: %q", s)
	}
	if b := hackPayload(t, a6, 8); le32(b, len(b)-4) != 0x00c22cc0+0x68 {
		t.Errorf("lobby 8 with hack widescreen: return address %08x", le32(b, len(b)-4))
	}
	if s := explain(a6, 3); s != "<B>Lobby 03<END>" {
		t.Errorf("lobby 3 without a hack: explain %q", s)
	}

	battleRule(5, 1, 90)
	battleRule(7, 600, 210)
}

func TestLobbyHackNoticeOff(t *testing.T) {
	conn := UseTestDB(t)
	addr := startLobby(t, NewMcs())
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES
		('console', 1, '', '', '', 'zproxy_test,chat_command'),
		('emu', 1, '', '', '', 'zproxy_test,chat_command'),
		('console', 4, '', '', '', 'widescreen')`); err != nil {
		t.Fatal(err)
	}
	serverLines := func(c *testClient) []string {
		c.ask(0x6408, nil)
		var got []string
		for _, m := range c.queue {
			if m.Command != 0x6702 {
				continue
			}
			r := m.Reader()
			if r.ReadString() == "SERVER" {
				r.ReadString()
				got = append(got, r.ReadString())
			}
		}
		c.queue = nil
		return got
	}
	for _, emu := range []bool{true, false} {
		c := dialLobby(t, addr)
		if emu {
			m := NewClientNotice(0x9950)
			m.Writer().Write([]byte("emulator=pcsx2\nsync=00000000000000aa\n"))
			c.send(m)
		}
		userID, _, _ := c.login(registerAccount(t), "******", "pilot")
		c.ask(0x6305, u16(1))
		c.ask(0x640F, u16(1))
		notice := serverLines(c)
		c.ask(0x6305, u16(1))
		c.chat("ｗ１", 0)
		reply := serverLines(c)
		u, err := DefaultDB.GetUser(userID)
		if err != nil {
			t.Fatal(err)
		}
		if emu && (len(notice) != 2 || len(reply) != 0 || u.System != 0) {
			t.Errorf("emu without a widescreen lobby: notice %q, w1 reply %q, system %x", notice, reply, u.System)
		}
		if !emu && (len(notice) != 5 || len(reply) != 3 || u.System != 1) {
			t.Errorf("console: notice %q, w1 reply %q, system %x", notice, reply, u.System)
		}
	}
}

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
	n := NewClientNotice(0x6701)
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
		r := c.expect(0x6702, CategoryNotice).Reader()
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
	conn := UseTestDB(t)
	addr := startLobby(t, NewMcs())
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES
		('console', 1, '', '', '', 'zproxy_test,chat_command'),
		('console', 3, '', '', '', 'pad_delay'),
		('console', 4, '', '', '', 'widescreen')`); err != nil {
		t.Fatal(err)
	}
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
		u, err := DefaultDB.GetUser(userID)
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
	if a := c.ask(0x6305, u16(2)); a.Status != StatusSuccess {
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
