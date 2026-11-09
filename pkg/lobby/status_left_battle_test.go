package lobby

import (
	"net"
	"testing"
	"time"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db/dbtest"
	"zdxsv/pkg/lobby/lobbyrpc"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

// A client leaves for a battle and never comes back with its battle session: the user logs in again
// with a new session, enters a lobby and logs out. /lbs/status must not list them in a battle.
func TestStatusAfterLeftBattleRelogin(t *testing.T) {
	conn := dbtest.Use(t)
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('console', 1, '', '', '', 'zproxy_test')`); err != nil {
		t.Fatal(err)
	}
	addr, app := startLobbyApp(t, battlerpc.NewBattleRpc())
	rpcc := startLobbyRPC(t, app)
	ac := registerAccount(t)
	c := dialLobby(t, addr)
	userID, _, _ := c.login(ac, "******", "pilot")
	c.ask(0x6305, u16(1))
	if res := call(t, rpcc, &lobbyrpc.RegisterProxyRequest{
		CurrentVersion: requiredVersion,
		UserID:         userID,
		LocalIP:        net.IPv4(192, 168, 0, 10),
		Port:           9000,
		UDPAddrs:       []string{"192.168.0.10:9000"},
	}).(*lobbyrpc.RegisterProxyResponse); !res.Result {
		t.Fatalf("register proxy: %+v", res)
	}
	c.ask(0x640E, []byte{model.EntryAeug})
	c.expect(0x6910, CategoryNotice)
	c.ask(0x6002, nil)
	c.conn.Close()

	c = dialLobby(t, addr)
	if got, _, _ := c.login(loginAgain(ac.LoginKey), userID, "pilot"); got != userID {
		t.Fatalf("relogin as %q", got)
	}
	c.ask(0x6305, u16(1))
	if s := MakeStatus(app.Status(), time.Now()); s.LobbyUserCount != 1 || s.BattleUserCount != 0 {
		t.Fatalf("in the lobby: %+v", s)
	}

	c.ask(0x6006, nil)
	c.conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := MakeStatus(app.Status(), time.Now())
		if s.LobbyUserCount == 0 {
			if s.BattleUserCount != 0 {
				t.Fatalf("after the logout: %+v", s)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("still in the lobby: %+v", s)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
