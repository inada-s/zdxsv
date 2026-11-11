package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
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
