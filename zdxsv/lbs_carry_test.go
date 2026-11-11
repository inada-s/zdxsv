package main

import (
	"strings"
	"testing"
	"time"
)

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
