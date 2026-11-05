package lobby

import (
	"strings"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
)

func TestCarryCodeCommand(t *testing.T) {
	dbtest.Use(t)
	var issuedFor []string
	addr, _ := startLobbyAppWith(t, battlerpc.NewBattleRpc(), func(a *App) {
		a.SetCarryCodeIssuer(func(loginKey string) string {
			issuedFor = append(issuedFor, loginKey)
			return "CARRY234"
		})
	})
	var cs []*testClient
	var keys []string
	for i := 0; i < 2; i++ {
		c := dialLobby(t, addr)
		ac := registerAccount(t)
		c.login(ac, "******", "pilot")
		c.ask(0x6305, u16(4))
		cs = append(cs, c)
		keys = append(keys, ac.LoginKey)
	}

	cs[0].say(sjis(t, carryCommand))
	got := cs[0].heard()
	if len(issuedFor) != 1 || issuedFor[0] != keys[0] || len(got) == 0 || !strings.Contains(got[0], "CARRY234") {
		t.Fatalf("issued for %v, sender heard %q", issuedFor, got)
	}
	if other := cs[1].heard(); len(other) != 0 {
		t.Fatalf("other player heard %q", other)
	}

	if err := db.DefaultDB.AddBan(&db.Ban{Kind: db.BanIP, Value: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	cs[0].say(sjis(t, carryCommand))
	if got := cs[0].heard(); len(issuedFor) != 1 || len(got) != 1 || strings.Contains(got[0], "CARRY234") {
		t.Fatalf("banned: issued for %v, heard %q", issuedFor, got)
	}
}

func TestTempKeyLobbyLogin(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())
	ac := registerAccount(t)
	temp, err := db.DefaultDB.RotateTempKey(ac.LoginKey)
	if err != nil {
		t.Fatal(err)
	}
	c := dialLobby(t, addr)
	byTemp := *ac
	byTemp.LoginKey = temp
	c.login(&byTemp, "******", "pilot")
	users, err := db.DefaultDB.GetUserList(ac.LoginKey)
	if err != nil || len(users) == 0 {
		t.Fatalf("no user registered under the account: %v %v", users, err)
	}
}
