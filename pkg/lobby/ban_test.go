package lobby

import (
	"strings"
	"testing"
	"time"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
)

func TestLobbyBan(t *testing.T) {
	dbtest.Use(t)
	addr, app := startLobbyApp(t, battlerpc.NewBattleRpc())
	ban := func(bans ...*db.MasterBan) {
		t.Helper()
		if err := db.DefaultDB.ReplaceMaster(&db.Master{Bans: bans}); err != nil {
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

	ban(&db.MasterBan{Key: u1, Until: time.Now().Add(time.Hour), Reason: "cheat"})
	refused(ac1)
	dialLobby(t, addr).login(ac2, "******", "two")

	ban(&db.MasterBan{Key: u1, Until: time.Now().Add(-time.Second)})
	c1.conn.Close()
	waitFor(t, "the lobby to drop "+u1, func() (gone bool) {
		app.Locked(func(a *App) { _, ok := a.users[u1]; gone = !ok })
		return
	})
	dialLobby(t, addr).login(ac1, u1, "one")
}
