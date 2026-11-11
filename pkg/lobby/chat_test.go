package lobby

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	"zdxsv/pkg/lobby/message"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

// say sends a chat line. The lobby does not answer it.
func (c *testClient) say(line string) {
	c.t.Helper()
	n := NewClientNotice(0x6701)
	n.Writer().Write(encryptString(n.Seq, line))
	c.send(n)
}

// heard returns "<user ID>:<line>" of each chat notice the lobby sent the
// client before it answers an echo sent now.
func (c *testClient) heard() []string {
	c.t.Helper()
	c.ask(0x600E, nil)
	var got []string
	rest := c.queue[:0]
	for _, m := range c.queue {
		if m.Command != 0x6702 || m.Category != CategoryNotice {
			rest = append(rest, m)
			continue
		}
		r := m.Reader()
		userID := r.ReadString()
		r.ReadString() // name
		got = append(got, userID+":"+r.ReadString())
	}
	c.queue = rest
	return got
}

func TestLobbyChatScope(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())

	const lobbyID, roomID = 4, 1
	var cs []*testClient
	var ids []string
	for i := 0; i < 3; i++ {
		c := dialLobby(t, addr)
		id, _, _ := c.login(registerAccount(t), "******", fmt.Sprintf("pilot%d", i))
		c.ask(0x6305, u16(lobbyID))
		cs = append(cs, c)
		ids = append(ids, id)
	}
	a, b, owner := cs[0], cs[1], cs[2]

	// say sends line from cs[from]; want[i] lists what cs[i] hears.
	// The sender is read first: its echo answer means the lobby has
	// handled the line before any other client sends its echo.
	say := func(step string, from int, line string, want ...[]string) {
		t.Helper()
		cs[from].say(line)
		order := []int{from}
		for i := range cs {
			if i != from {
				order = append(order, i)
			}
		}
		for _, i := range order {
			if got := cs[i].heard(); fmt.Sprint(got) != fmt.Sprint(want[i]) {
				t.Errorf("%s: pilot%d heard %v, want %v", step, i, got, want[i])
			}
		}
	}
	line := func(from int, s string) []string { return []string{ids[from] + ":" + s} }

	if a := owner.ask(0x6407, u16(roomID)); a.Status != StatusSuccess {
		t.Fatalf("RequestCreateRoom status %x", a.Status)
	}
	q := NewClientQuestion(0x6609)
	q.Writer().Write(encryptString(q.Seq, "chatroom"))
	owner.send(q)
	owner.expect(0x6609, CategoryAnswer)
	owner.ask(0x660C, nil)

	say("lobby, owner in room", 0, "l1", line(0, "l1"), line(0, "l1"), nil)
	say("room alone", 2, "r1", nil, nil, line(2, "r1"))

	b.ask(0x6406, append(u16(roomID), 0, 0, 0, 0))
	owner.expect(0x6503, CategoryNotice)
	say("room of two, owner", 2, "r2", nil, line(2, "r2"), line(2, "r2"))
	say("room of two, guest", 1, "r3", nil, line(1, "r3"), line(1, "r3"))

	b.ask(0x6501, nil)
	if left := owner.expect(0x6502, CategoryNotice).Reader().ReadString(); left != ids[1] {
		t.Errorf("owner saw %q leave", left)
	}
	say("guest back in lobby", 0, "l2", line(0, "l2"), line(0, "l2"), nil)

	b.ask(0x6406, append(u16(roomID), 0, 0, 0, 0))
	owner.expect(0x6503, CategoryNotice)
	owner.ask(0x6501, nil)
	b.expect(0x6505, CategoryNotice)
	if s := roomStatus(a, roomID); s != model.RoomStateEmpty {
		t.Errorf("room status after the owner left %d", s)
	}
	say("room removed", 1, "l3", line(1, "l3"), line(1, "l3"), line(1, "l3"))
}

// A zproxy_test lobby starts a one player test battle on entry, which needs a UDP proxy
// registered within the last 20 seconds.
func TestLobbyTestBattleWithoutProxy(t *testing.T) {
	conn := dbtest.Use(t)
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('console', 1, '', '', '', 'zproxy_test')`); err != nil {
		t.Fatal(err)
	}
	addr := startLobby(t, battlerpc.NewBattleRpc())

	c := dialLobby(t, addr)
	c.login(registerAccount(t), "******", "pilot")
	c.ask(0x6305, u16(1))
	c.ask(0x640E, []byte{model.EntryAeug})

	want, err := japanese.ShiftJIS.NewEncoder().String("SERVER:UDPプロキシが登録されていません")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.heard(); fmt.Sprint(got) != fmt.Sprint([]string{want}) {
		t.Errorf("heard %q, want %q", got, want)
	}
	for _, m := range c.queue {
		if m.Command == 0x6910 {
			t.Errorf("battle start notice without a proxy")
		}
	}
}

// Each chat notice ends with 4 zero bytes after the text: type and the ID, name
// and text colors, which mean the default colors.
func TestChatNoticeColorBytes(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())

	c := dialLobby(t, addr)
	c.login(registerAccount(t), "******", "pilot")
	c.ask(0x6305, u16(4))
	c.say("11111111")
	c.say("a")
	c.ask(0x600E, nil)

	n := 0
	for _, m := range c.queue {
		if m.Command != 0x6702 || m.Category != CategoryNotice {
			continue
		}
		n++
		r := m.Reader()
		r.ReadString()
		r.ReadString()
		text := r.ReadString()
		if r.Remaining() != 4 {
			t.Fatalf("%q: %d bytes after the text, want 4", text, r.Remaining())
		}
		for i := 0; i < 4; i++ {
			if b := r.Read8(); b != 0 {
				t.Errorf("%q: byte %d after the text is %#x, want 0", text, i, b)
			}
		}
	}
	if n != 2 {
		t.Fatalf("got %d chat notices, want 2", n)
	}
}

func TestLobbyChatDropsTextLongerThanGameInput(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())
	var cs []*testClient
	var ids []string
	for _, name := range []string{"pilot0", "pilot1"} {
		c := dialLobby(t, addr)
		id, _, _ := c.login(registerAccount(t), "******", name)
		c.ask(0x6305, u16(4))
		c.heard()
		cs = append(cs, c)
		ids = append(ids, id)
	}
	for _, tc := range []struct {
		line  string
		heard bool
	}{
		{strings.Repeat("あ", 20), true},
		{strings.Repeat("あ", 21), false},
		{strings.Repeat("あ", 26), false},
		{strings.Repeat("a", 40), true},
		{strings.Repeat("a", 41), false},
	} {
		cs[0].say(sjis(t, tc.line))
		for i, c := range cs {
			got := c.heard()
			want := 0
			if tc.heard {
				want = 1
			}
			if len(got) != want || (want == 1 && got[0] != ids[0]+":"+sjis(t, tc.line)) {
				t.Errorf("%d chars, client %d heard %q", len([]rune(tc.line)), i, got)
			}
		}
	}
}

// addPeer adds a logged in peer to app; sent messages stay in its Conn's outbuf.
func addPeer(t *testing.T, app *App, userID string) *AppPeer {
	t.Helper()
	c, _ := newPipe(t)
	p := &AppPeer{app: app, conn: NewConn(c), Platform: model.PlatformConsole}
	p.User = model.User{User: db.User{UserID: userID, Name: userID, SessionID: "S" + userID}}
	app.users[userID] = p
	return p
}

// chatLines returns the chat lines in p's unsent output and clears it.
func chatLines(p *AppPeer) []string {
	var got []string
	buf := p.conn.outbuf
	for len(buf) > 0 {
		n, m := message.Deserialize(buf)
		if n == 0 {
			break
		}
		buf = buf[n:]
		if m.Command == 0x6702 {
			r := m.Reader()
			from := r.ReadString()
			r.ReadString()
			got = append(got, from+":"+r.ReadString())
		}
	}
	p.conn.outbuf = p.conn.outbuf[:0]
	return got
}

// Chat in the battle after room reaches only the battle's users in that
// room, not a battle user still elsewhere.
func TestChatInBattleAfterRoom(t *testing.T) {
	app := NewApp()
	a, b, c := addPeer(t, app, "A"), addPeer(t, app, "B"), addPeer(t, app, "C")
	battle := model.NewBattle(model.PlatformConsole, 0)
	for _, p := range []*AppPeer{a, b, c} {
		battle.Add(&p.User)
		app.battles[p.SessionID] = battle
	}
	app.OnEnterBattleAfterRoom(a)
	app.OnEnterBattleAfterRoom(b)
	c.Battle = battle
	for _, p := range []*AppPeer{a, b, c} {
		chatLines(p)
	}

	app.OnSendChatMessage(a, "gg")
	for _, w := range []struct {
		p    *AppPeer
		want string
	}{{a, "[A:gg]"}, {b, "[A:gg]"}, {c, "[]"}} {
		if got := chatLines(w.p); fmt.Sprint(got) != w.want {
			t.Errorf("%s heard %v, want %s", w.p.UserID, got, w.want)
		}
	}
}
