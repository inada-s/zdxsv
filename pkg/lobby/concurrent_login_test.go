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
	var p *AppPeer
	var inLobby bool
	app.Locked(func(a *App) {
		p = a.users[u1]
		inLobby = p != nil && p.Lobby != nil && p.Lobby.Users[u1] == &p.User && first.Lobby == nil
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
