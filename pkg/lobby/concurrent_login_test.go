package lobby

import (
	"strings"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
)

func TestAccountUsersLogInAtOnce(t *testing.T) {
	dbtest.Use(t)
	addr, app := startLobbyApp(t, battlerpc.NewBattleRpc())
	loggedIn := func(userID string) (peer *AppPeer) {
		app.Locked(func(a *App) { peer = a.users[userID] })
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

	dup := dialLobby(t, addr)
	dup.chooseUser(loginAgain(ac.LoginKey), u1, "one")
	if msg := dup.expect(0x6003, CategoryNotice).Reader().ReadString(); !strings.Contains(msg, sjis(t, "使用中")) {
		t.Fatalf("refusal %q", msg)
	}
	dup.ask(0x600E, nil)
	for _, m := range dup.queue {
		if m.Command == 0x6104 || m.Command == 0x6132 {
			t.Fatalf("login of a user in use went on: 0x%04x", m.Command)
		}
	}
	dup.conn.Close()
	waitFor(t, "the lobby to drop the refused client", func() (gone bool) {
		app.Locked(func(a *App) { gone = len(a.peers) == 3 })
		return
	})
	if loggedIn(u1) != first {
		t.Fatal("the refused client logged out the user in use")
	}

	other := dialLobby(t, addr)
	other.chooseUser(registerAccount(t), u2, "two")
	other.ask(0x600E, nil)
	for _, m := range other.queue {
		if m.Command == 0x6104 || m.Command == 0x6132 {
			t.Fatalf("logged in as a user of another account: 0x%04x", m.Command)
		}
	}

	c1.conn.Close()
	waitFor(t, "the lobby to drop "+u1, func() bool { return loggedIn(u1) == nil })
	dialLobby(t, addr).login(loginAgain(ac.LoginKey), u1, "one")
}
