package main

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
)

func TestLobbyBan(t *testing.T) {
	UseTestDB(t)
	addr, app := startLobbyApp(t, NewMcs())
	ban := func(bans ...*MasterBan) {
		t.Helper()
		if err := DefaultDB.ReplaceMaster(&Master{Bans: bans}); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(ac *testLogin) {
		t.Helper()
		c := dialLobby(t, addr)
		kp := c.expect(0x6101, CategoryQuestion)
		a := NewClientAnswer(kp)
		a.Writer().WriteString(keyPairKey1(ac.SessionID)).Write(encryptString(a.Seq, ac.LoginKey))
		c.send(a)
		if msg := c.expect(0x6003, CategoryNotice).Reader().ReadString(); !strings.Contains(msg, "BANNED") {
			t.Fatalf("refusal %q", msg)
		}
		c.ask(0x600E, nil)
		for _, m := range c.queue {
			if m.Command == 0x6103 {
				t.Fatal("first data asked after the refusal")
			}
		}
	}

	ac1 := registerAccount(t)
	c1 := dialLobby(t, addr)
	u1, _, _ := c1.login(ac1, "******", "one")
	ac2 := registerAccount(t)
	dialLobby(t, addr).login(ac2, "******", "two")

	ban(&MasterBan{Key: u1, Until: time.Now().Add(time.Hour), Reason: "cheat"})
	refused(ac1)
	dialLobby(t, addr).login(ac2, "******", "two")

	ban(&MasterBan{Key: u1, Until: time.Now().Add(-time.Second)})
	c1.conn.Close()
	waitFor(t, "the lobby to drop "+u1, func() (gone bool) {
		app.Locked(func(a *Lbs) { _, ok := a.users[u1]; gone = !ok })
		return
	})
	dialLobby(t, addr).login(ac1, u1, "one")
}

func TestAccountUsersLogInAtOnce(t *testing.T) {
	UseTestDB(t)
	addr, app := startLobbyApp(t, NewMcs())
	loggedIn := func(userID string) (peer *LbsPeer) {
		app.Locked(func(a *Lbs) { peer = a.users[userID] })
		return
	}

	ac := registerAccount(t)
	c1 := dialLobby(t, addr)
	u1, _, _ := c1.login(ac, "******", "one")
	ac2 := loginAgain(ac.LoginKey)
	u2, listed, _ := dialLobby(t, addr).login(ac2, "******", "two")
	if len(listed) != 1 || listed[0] != u1 || u2 == u1 {
		t.Fatalf("second client of the account: user %s, listed %v", u2, listed)
	}
	if loggedIn(u1) == nil || loggedIn(u2) == nil {
		t.Fatal("a login of the account logged out the other user")
	}
	first := loggedIn(u1)

	// The first client still holds its session after the account's later logins.
	dialLobby(t, addr).login(ac, "******", "three")

	// A client whose line dropped without FIN still holds its user; the user's next login closes it.
	const lobbyID = 4
	c1.ask(0x6305, u16(lobbyID))
	again := dialLobby(t, addr)
	if u, _, _ := again.login(loginAgain(ac.LoginKey), u1, "one"); u != u1 {
		t.Fatalf("login again as %s: %s", u1, u)
	}
	if msg := c1.expect(0x6003, CategoryNotice).Reader().ReadString(); !strings.Contains(msg, sjis(t, "シャットダウン")) {
		t.Fatalf("notice to the closed client %q", msg)
	}
	expectClosed(t, c1)
	again.ask(0x6305, u16(lobbyID))
	queued := NewClientQuestion(0x6305)
	queued.Writer().Write(u16(lobbyID))
	app.chEvent <- eventPeerMessage{peer: first, msg: queued}
	var p *LbsPeer
	var inLobby bool
	app.Locked(func(a *Lbs) {
		p = a.users[u1]
		inLobby = p != nil && p.Lobby != nil && p.Lobby.Users[u1] == &p.LbsUser && first.Lobby == nil
	})
	if p == nil || p == first {
		t.Fatalf("%s held by %v after its new login", u1, p)
	}
	if !inLobby {
		t.Fatal("the closed client took the new client's place in the lobby")
	}

	other := dialLobby(t, addr)
	other.chooseUser(registerAccount(t), u2, "two")
	other.ask(0x600E, nil)
	for _, m := range other.queue {
		if m.Command == 0x6104 || m.Command == 0x6132 {
			t.Fatalf("logged in as a user of another account: 0x%04x", m.Command)
		}
	}

	again.conn.Close()
	waitFor(t, "the lobby to drop "+u1, func() bool { return loggedIn(u1) == nil })
	dialLobby(t, addr).login(loginAgain(ac.LoginKey), u1, "one")
}

func TestAccountUserLimit(t *testing.T) {
	sqldb := UseTestDB(t)
	addr, _ := startLobbyApp(t, NewMcs())
	countUsers := func(loginKey string) (n int) {
		if err := sqldb.Get(&n, `SELECT COUNT(*) FROM user WHERE login_key = ?`, loginKey); err != nil {
			t.Fatal(err)
		}
		return
	}
	refused := func(c *testClient, what string) {
		t.Helper()
		c.ask(0x600E, nil)
		for _, m := range c.queue {
			if m.Command == 0x6104 || m.Command == 0x6132 {
				t.Fatalf("%s: logged in, 0x%04x", what, m.Command)
			}
		}
	}

	ac := registerAccount(t)
	var made []string
	for i := 0; i < MaxUsersPerAccount; i++ {
		u, _, _ := dialLobby(t, addr).login(loginAgain(ac.LoginKey), "******", "new")
		made = append(made, u)
	}

	c := dialLobby(t, addr)
	listed := c.chooseUser(loginAgain(ac.LoginKey), "******", "new")
	refused(c, "user over the limit")
	if n := countUsers(ac.LoginKey); n != MaxUsersPerAccount {
		t.Fatalf("%d users after a refused registration", n)
	}
	sort.Strings(made)
	sort.Strings(listed)
	if len(listed) != len(made) {
		t.Fatalf("listed %v, made %v", listed, made)
	}
	for i := range made {
		if listed[i] != made[i] {
			t.Fatalf("listed %v, made %v", listed, made)
		}
	}
	for _, u := range made {
		if got, _, _ := dialLobby(t, addr).login(loginAgain(ac.LoginKey), u, "old"); got != u {
			t.Fatalf("select %s: logged in as %s", u, got)
		}
	}

	over := registerAccount(t)
	var overUsers []string
	for _, id := range []string{"OVER01", "OVER02", "OVER03", "OVER04"} {
		if _, err := sqldb.Exec(`INSERT INTO user (user_id, login_key, created) VALUES (?, ?, ?)`, id, over.LoginKey, time.Now()); err != nil {
			t.Fatal(err)
		}
		overUsers = append(overUsers, id)
	}
	c = dialLobby(t, addr)
	if listed := c.chooseUser(loginAgain(over.LoginKey), "******", "new"); len(listed) != len(overUsers) {
		t.Fatalf("account over the limit: listed %v", listed)
	}
	refused(c, "account over the limit")
	if n := countUsers(over.LoginKey); n != len(overUsers) {
		t.Fatalf("%d users on the account over the limit", n)
	}
	if got, _, _ := dialLobby(t, addr).login(loginAgain(over.LoginKey), "OVER04", "old"); got != "OVER04" {
		t.Fatalf("account over the limit: logged in as %s", got)
	}
}

func TestUserOfAnotherAccountRefused(t *testing.T) {
	UseTestDB(t)
	addr, app := startLobbyApp(t, NewMcs())
	peerOf := func(userID string) (p *LbsPeer) {
		app.Locked(func(a *Lbs) { p = a.users[userID] })
		return
	}
	afterBattle := func(sessionID string, u DBUser) {
		u.SessionID = sessionID
		app.Locked(func(a *Lbs) {
			b := NewBattle(PlatformEmu, 0)
			b.Users = append(b.Users, LbsUser{DBUser: u})
			a.battles[sessionID] = b
		})
	}

	acB := registerAccount(t)
	userB, _, _ := dialLobby(t, addr).login(acB, "******", "bee")
	stored, err := DefaultDB.GetUser(userB)
	if err != nil {
		t.Fatal(err)
	}
	peerB := peerOf(userB)

	refused := func(path string, c *testClient) {
		t.Helper()
		c.ask(0x600E, nil)
		for _, m := range c.queue {
			if m.Command == 0x6104 || m.Command == 0x6132 {
				t.Fatalf("%s: logged in as %s of another account: 0x%04x", path, userB, m.Command)
			}
		}
		u, err := DefaultDB.GetUser(userB)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*u, *stored) {
			t.Fatalf("%s: stored user changed\n%+v\n%+v", path, *stored, *u)
		}
		if p := peerOf(userB); p != peerB {
			t.Fatalf("%s: %s held by %v", path, userB, p)
		}
	}

	acA := registerAccount(t)
	userA, _, _ := dialLobby(t, addr).login(acA, "******", "ay")

	c := dialLobby(t, addr)
	c.chooseUser(loginAgain(acA.LoginKey), userB, "evil")
	refused("choose", c)

	afterBattle(acB.SessionID, *stored)
	c = dialLobby(t, addr)
	kp := c.expect(0x6101, CategoryQuestion)
	a := NewClientAnswer(kp)
	a.Writer().WriteString(keyPairKey1(acB.SessionID)).Write(encryptString(a.Seq, acA.LoginKey))
	c.send(a)
	refused("session of another account", c)
	for _, m := range c.queue {
		if m.Command == 0x6103 || m.Command == 0x6131 {
			t.Fatalf("session of another account: 0x%04x", m.Command)
		}
	}

	ac := loginAgain(acA.LoginKey)
	afterBattle(ac.SessionID, *stored)
	c = dialLobby(t, addr)
	kp = c.expect(0x6101, CategoryQuestion)
	a = NewClientAnswer(kp)
	a.Writer().WriteString(keyPairKey1(ac.SessionID)).Write(encryptString(a.Seq, ac.LoginKey))
	c.send(a)
	c.send(NewClientAnswer(c.expect(0x6103, CategoryQuestion)))
	q := NewClientQuestion(0x6132)
	q.Writer().Write(append(encryptString(q.Seq, userB), encryptString(q.Seq, "bee")...))
	c.send(q)
	refused("battle user of another account", c)
	for _, m := range c.queue {
		if m.Command == 0x6131 {
			t.Fatal("battle user of another account listed")
		}
	}

	ownA, err := DefaultDB.GetUser(userA)
	if err != nil {
		t.Fatal(err)
	}
	ac = loginAgain(acA.LoginKey)
	afterBattle(ac.SessionID, *ownA)
	if u, _, _ := dialLobby(t, addr).login(ac, userA, "ay"); u != userA {
		t.Fatalf("own user after battle: %s", u)
	}
}

func TestLobbyOutdatedClient(t *testing.T) {
	conn := UseTestDB(t)
	addr := startLobby(t, NewMcs())
	set := func(key, v string) {
		t.Helper()
		if _, err := conn.Exec(`INSERT OR REPLACE INTO m_server_setting (key, value) VALUES (?, ?)`, key, v); err != nil {
			t.Fatal(err)
		}
	}
	dial := func(info string) *testClient {
		c := dialLobby(t, addr)
		if info != "" {
			n := NewClientNotice(0x9950)
			n.Writer().Write([]byte(info))
			c.send(n)
		}
		return c
	}
	refusedAs := func(ac *testLogin, info string) {
		t.Helper()
		c := dial(info)
		kp := c.expect(0x6101, CategoryQuestion)
		a := NewClientAnswer(kp)
		a.Writer().WriteString(keyPairKey1(ac.SessionID)).Write(encryptString(a.Seq, ac.LoginKey))
		c.send(a)
		if msg := c.expect(0x6003, CategoryNotice).Reader().ReadString(); !strings.Contains(msg, "PLEASE UPDATE PCSX2") {
			t.Fatalf("%q: refusal %q", info, msg)
		}
		c.ask(0x600E, nil)
		for _, m := range c.queue {
			if m.Command == 0x6103 {
				t.Fatalf("%q: first data asked after the refusal", info)
			}
		}
	}
	refused := func(info string) { t.Helper(); refusedAs(registerAccount(t), info) }
	allowedAs := func(ac *testLogin, info string) {
		t.Helper()
		dial(info).login(ac, "******", "p")
	}
	allowed := func(info string) { t.Helper(); allowedAs(registerAccount(t), info) }
	const emu = "emulator=pcsx2\n"

	allowed(emu + "pcsx2=zdxsv-0.0.1\n")
	allowed(emu + "pcsx2=zdxsv-1.2.1-dev.1\n")
	allowed(emu + "version=abc\n")
	set("min_pcsx2_version", "zdxsv-1.2.0")
	refused(emu + "pcsx2=zdxsv-1.1.9\n")
	refused(emu + "pcsx2=zdxsv-1.2.0-dev.3\n")
	refused(emu + "pcsx2=garbage\n")
	refused(emu + "pcsx2=zdxsv-1.2.1-dev.1\n")
	refused(emu + "version=abc\n")
	refused("emulator=other\n")
	allowed(emu + "pcsx2=zdxsv-1.2.0\n")
	allowed(emu + "pcsx2=zdxsv-1.10.0\n")
	allowed("")
	allowed("os=windows\n")

	dev, other := registerAccount(t), registerAccount(t)
	refusedAs(dev, emu+"version=abc\n")
	set("pcsx2_allow", "x, "+dev.LoginKey+",mach-1")
	allowedAs(dev, emu+"version=abc\n")
	allowedAs(dev, emu+"pcsx2=zdxsv-1.1.9\n")
	refusedAs(other, emu+"version=abc\n")
	allowed(emu + "machine_id=mach-1\nversion=abc\n")
	refused(emu + "machine_id=mach-2\nversion=abc\n")

	set("min_pcsx2_version", "1.3")
	allowed(emu + "pcsx2=zdxsv-1.1.9\n")
}

func TestCarryCodeCommand(t *testing.T) {
	conn := UseTestDB(t)
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('console', 1, '', '', '', 'chat_command')`); err != nil {
		t.Fatal(err)
	}
	var issuedFor []string
	addr, _ := startLobbyAppWith(t, NewMcs(), func(a *Lbs) {
		a.SetCarryCodeIssuer(func(loginKey string) string {
			issuedFor = append(issuedFor, loginKey)
			return "CARRY234"
		})
	})
	var cs []*testClient
	var keys []string
	for _, lobby := range []uint16{1, 1, 4} {
		c := dialLobby(t, addr)
		ac := registerAccount(t)
		c.login(ac, "******", "pilot")
		c.ask(0x6305, u16(lobby))
		c.heard()
		cs = append(cs, c)
		keys = append(keys, ac.LoginKey)
	}

	cs[0].say(sjis(t, carryCommand))
	if got := cs[0].heard(); len(issuedFor) != 0 || len(got) != 1 || strings.Contains(got[0], "CARRY234") {
		t.Fatalf("under %d battles: issued for %v, heard %q", CarryMinBattles, issuedFor, got)
	}
	for _, k := range keys {
		users, err := DefaultDB.GetUserList(k)
		if err != nil || len(users) == 0 {
			t.Fatal("no user", k, err)
		}
		users[0].BattleCount = CarryMinBattles
		if err := DefaultDB.UpdateUser(users[0]); err != nil {
			t.Fatal(err)
		}
	}
	cs[2].say(sjis(t, carryCommand))
	if len(issuedFor) != 0 {
		t.Fatalf("issued outside lobby 1 for %v", issuedFor)
	}
	cs[0].say(sjis(t, carryCommand))
	got := cs[0].heard()
	if len(issuedFor) != 1 || issuedFor[0] != keys[0] || len(got) == 0 || !strings.Contains(got[0], "CARRY234") {
		t.Fatalf("issued for %v, sender heard %q", issuedFor, got)
	}
	if other := cs[1].heard(); len(other) != 0 {
		t.Fatalf("other player heard %q", other)
	}

	users, _ := DefaultDB.GetUserList(keys[0])
	if err := DefaultDB.ReplaceMaster(&Master{Bans: []*MasterBan{{Key: users[0].UserID, Until: time.Now().Add(time.Hour)}}}); err != nil {
		t.Fatal(err)
	}
	cs[0].say(sjis(t, carryCommand))
	if got := cs[0].heard(); len(issuedFor) != 1 || len(got) != 1 || strings.Contains(got[0], "CARRY234") {
		t.Fatalf("banned: issued for %v, heard %q", issuedFor, got)
	}
}

func TestTempKeyLobbyLogin(t *testing.T) {
	UseTestDB(t)
	addr := startLobby(t, NewMcs())
	ac := registerAccount(t)
	id, temp := Sessions.Start(ac.LoginKey, true)
	c := dialLobby(t, addr)
	c.login(&testLogin{LoginKey: temp, SessionID: id}, "******", "pilot")
	users, err := DefaultDB.GetUserList(ac.LoginKey)
	if err != nil || len(users) == 0 {
		t.Fatalf("no user registered under the account: %v %v", users, err)
	}
}

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
	UseTestDB(t)
	addr := startLobby(t, NewMcs())

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
	if s := roomStatus(a, roomID); s != RoomStateEmpty {
		t.Errorf("room status after the owner left %d", s)
	}
	say("room removed", 1, "l3", line(1, "l3"), line(1, "l3"), line(1, "l3"))
}

// A zproxy_test lobby starts a one player test battle on entry, which needs a UDP proxy
// registered within the last 20 seconds.
func TestLobbyTestBattleWithoutProxy(t *testing.T) {
	conn := UseTestDB(t)
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('console', 1, '', '', '', 'zproxy_test')`); err != nil {
		t.Fatal(err)
	}
	addr := startLobby(t, NewMcs())

	c := dialLobby(t, addr)
	c.login(registerAccount(t), "******", "pilot")
	c.ask(0x6305, u16(1))
	c.ask(0x640E, []byte{EntryAeug})

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
	UseTestDB(t)
	addr := startLobby(t, NewMcs())

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
	UseTestDB(t)
	addr := startLobby(t, NewMcs())
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
func addPeer(t *testing.T, app *Lbs, userID string) *LbsPeer {
	t.Helper()
	c, _ := newPipe(t)
	p := &LbsPeer{app: app, conn: NewLbsConn(c), Platform: PlatformConsole}
	p.LbsUser = LbsUser{DBUser: DBUser{UserID: userID, Name: userID, SessionID: "S" + userID}}
	app.users[userID] = p
	return p
}

// chatLines returns the chat lines in p's unsent output and clears it.
func chatLines(p *LbsPeer) []string {
	var got []string
	buf := p.conn.outbuf
	for len(buf) > 0 {
		n, m := Deserialize(buf)
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
	app := NewLbs()
	a, b, c := addPeer(t, app, "A"), addPeer(t, app, "B"), addPeer(t, app, "C")
	battle := NewBattle(PlatformConsole, 0)
	for _, p := range []*LbsPeer{a, b, c} {
		battle.Add(&p.LbsUser)
		app.battles[p.SessionID] = battle
	}
	app.OnEnterBattleAfterRoom(a)
	app.OnEnterBattleAfterRoom(b)
	c.Battle = battle
	for _, p := range []*LbsPeer{a, b, c} {
		chatLines(p)
	}

	app.OnSendChatMessage(a, "gg")
	for _, w := range []struct {
		p    *LbsPeer
		want string
	}{{a, "[A:gg]"}, {b, "[A:gg]"}, {c, "[]"}} {
		if got := chatLines(w.p); fmt.Sprint(got) != w.want {
			t.Errorf("%s heard %v, want %s", w.p.UserID, got, w.want)
		}
	}
}

var reRankingRow = regexp.MustCompile(`(\d+)位\s*<TD[^>]*><FONT COLOR=#FFA100>(\w+)</FONT>`)

// rankingPage asks for an information page and returns its HTML and the
// "<rank>:<user ID>" of each row.
func (c *testClient) rankingPage(path string) (string, []string) {
	c.t.Helper()
	q := NewClientQuestion(0x6801)
	q.Writer().Write(encryptString(q.Seq, path))
	c.send(q)
	r := c.expect(0x6801, CategoryAnswer).Reader()
	if got := r.ReadString(); got != path {
		c.t.Fatalf("answer path %q, want %q", got, path)
	}
	html, err := japanese.ShiftJIS.NewDecoder().String(r.ReadString())
	if err != nil {
		c.t.Fatal(err)
	}
	var rows []string
	for _, m := range reRankingRow.FindAllStringSubmatch(html, -1) {
		rows = append(rows, m[1]+":"+m[2])
	}
	return html, rows
}

func TestLobbyRankingPages(t *testing.T) {
	UseTestDB(t)
	addr := startLobby(t, NewMcs())

	// 12 ranked users: user i has i+1 wins, 12-i Aeug wins and 2i kills.
	var ids []string
	for i := 0; i < 12; i++ {
		u, err := DefaultDB.RegisterUser(fmt.Sprint("rank", i))
		if err != nil {
			t.Fatal(err)
		}
		u.Name = fmt.Sprint("pilot", i)
		u.BattleCount, u.WinCount, u.LoseCount = 20, i+1, 3
		u.AeugBattleCount, u.AeugWinCount, u.AeugLoseCount = 20, 12-i, 1
		u.KillCount = 2 * i
		if err := DefaultDB.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.UserID)
	}
	c := dialLobby(t, addr)
	c.login(registerAccount(t), "******", "viewer") // 13th user, all counts 0

	rows := func(rank int, users ...int) []string {
		var r []string
		for _, i := range users {
			r = append(r, fmt.Sprint(rank, ":", ids[i]))
			rank++
		}
		return r
	}
	// rows lists the first rows of a page; n is its row count. Ties share a
	// rank in any order: all 13 Titans kill counts are 0, user 0 and the
	// viewer have 0 kills.
	cases := []struct {
		path       string
		rows       []string
		n          int
		prev, next string // link targets, "" = none
		text       string
	}{
		{"06/V_RANK/TOTAL00.HTM", rows(1, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2), 10, "", "TOTAL01", "   20戦    12勝     3敗 (無効:     5)"},
		{"06/V_RANK/TOTAL01.HTM", rows(11, 1, 0), 3, "TOTAL00", "", "勝利数ランキング(総合)"},
		{"06/V_RANK/TOTAL05.HTM", nil, 0, "TOTAL04", "", ""},
		{"06/V_RANK/AEUG00.HTM", rows(1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9), 10, "", "AEUG01", "   20戦    12勝     1敗 (無効:     7)"},
		{"06/P_RANK/TITANS00.HTM", nil, 10, "", "TITANS01", "撃墜数ランキング(ジオン・ティターンズ)"},
		{"06/P_RANK/TOTAL01.HTM", rows(11, 1), 3, "TOTAL00", "", "撃墜数：2 機"},
	}
	for _, tc := range cases {
		html, got := c.rankingPage(tc.path)
		if len(got) != tc.n || fmt.Sprint(got[:len(tc.rows)]) != fmt.Sprint(tc.rows) {
			t.Errorf("%s rows %v\nwant %d rows starting %v", tc.path, got, tc.n, tc.rows)
		}
		links := 0
		for _, to := range []string{tc.prev, tc.next} {
			if to == "" {
				continue
			}
			links++
			if !strings.Contains(html, `<a href="`+to+`">`) {
				t.Errorf("%s: no link to %s", tc.path, to)
			}
		}
		if n := strings.Count(html, `<a href="`); n != links {
			t.Errorf("%s: %d links, want %d", tc.path, n, links)
		}
		if !strings.Contains(html, tc.text) {
			t.Errorf("%s does not contain %q", tc.path, tc.text)
		}
	}

	if html, _ := c.rankingPage("06/INFOR/INFOR00.HTM"); strings.Contains(html, "位") {
		t.Errorf("information page lists a ranking")
	}
}

// newRobustPeer returns a logged in peer of a new Lbs; sent messages stay in its Conn's outbuf.
func newRobustPeer(t *testing.T) *LbsPeer {
	t.Helper()
	c, _ := newPipe(t)
	app := NewLbs()
	p := &LbsPeer{app: app, conn: NewLbsConn(c), Platform: PlatformConsole}
	p.LbsUser = LbsUser{DBUser: DBUser{UserID: "ROBUST", Name: "robust"}}
	app.users[p.UserID] = p
	return p
}

// Every client message handler must survive any body in any peer state: a
// panic in a handler stops the whole lobby.
func TestHandlersSurviveBadInput(t *testing.T) {
	UseTestDB(t)
	states := []struct {
		name  string
		setup func(*LbsPeer)
	}{
		{"no lobby", func(*LbsPeer) {}},
		{"in lobby", func(p *LbsPeer) { p.app.OnEnterLobby(p, 4) }},
	}
	bodies := [][]byte{
		nil,
		{0, 1, 0xFF, 0xFF, 0xFF, 0xFF},
		bytes.Repeat([]byte{0xFF}, 16),
	}
	var cmds []int
	for cmd, name := range defaultHandlers.handlerNames {
		if name == "testHandler" { // added by TestDispatchMessage
			continue
		}
		cmds = append(cmds, int(cmd))
	}
	sort.Ints(cmds)

	for _, s := range states {
		for _, cmd := range cmds {
			for i, body := range bodies {
				name := fmt.Sprintf("%s/0x%04x %s/%d", s.name, cmd, defaultHandlers.handlerNames[uint16(cmd)], i)
				func() {
					p := newRobustPeer(t)
					s.setup(p)
					m := NewClientQuestion(uint16(cmd))
					m.Writer().Write(body)
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("%s: panic: %v", name, r)
						}
					}()
					defaultHandlers.handlers[uint16(cmd)](p, m)
				}()
			}
		}
	}
}
