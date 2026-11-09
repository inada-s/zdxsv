package lobby

import (
	"sort"
	"testing"
	"time"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
)

func TestAccountUserLimit(t *testing.T) {
	sqldb := dbtest.Use(t)
	addr, _ := startLobbyApp(t, battlerpc.NewBattleRpc())
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
	for i := 0; i < db.MaxUsersPerAccount; i++ {
		u, _, _ := dialLobby(t, addr).login(loginAgain(ac.LoginKey), "******", "new")
		made = append(made, u)
	}

	c := dialLobby(t, addr)
	listed := c.chooseUser(loginAgain(ac.LoginKey), "******", "new")
	refused(c, "user over the limit")
	if n := countUsers(ac.LoginKey); n != db.MaxUsersPerAccount {
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
