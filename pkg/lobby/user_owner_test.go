package lobby

import (
	"reflect"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

func TestUserOfAnotherAccountRefused(t *testing.T) {
	dbtest.Use(t)
	addr, app := startLobbyApp(t, battlerpc.NewBattleRpc())
	peerOf := func(userID string) (p *AppPeer) {
		app.Locked(func(a *App) { p = a.users[userID] })
		return
	}
	afterBattle := func(sessionID string, u db.User) {
		u.SessionID = sessionID
		app.Locked(func(a *App) {
			b := model.NewBattle(model.PlatformEmu, 0)
			b.Users = append(b.Users, model.User{User: u})
			a.battles[sessionID] = b
		})
	}

	acB := registerAccount(t)
	userB, _, _ := dialLobby(t, addr).login(acB, "******", "bee")
	stored, err := db.DefaultDB.GetUser(userB)
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
		u, err := db.DefaultDB.GetUser(userB)
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

	ownA, err := db.DefaultDB.GetUser(userA)
	if err != nil {
		t.Fatal(err)
	}
	ac = loginAgain(acA.LoginKey)
	afterBattle(ac.SessionID, *ownA)
	if u, _, _ := dialLobby(t, addr).login(ac, userA, "ay"); u != userA {
		t.Fatalf("own user after battle: %s", u)
	}
}
