package lobby

import (
	"fmt"
	"net"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	"zdxsv/pkg/lobby/lobbyrpc"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

// lobby1Proxy logs a user in, enters lobby 1 and registers its proxy over RPC.
func lobby1Proxy(t *testing.T) (*App, *testClient, *db.Account, string) {
	t.Helper()
	dbtest.Use(t)
	addr, app := startLobbyApp(t, battlerpc.NewBattleRpc())
	rpcc := startLobbyRPC(t, app)
	ac := registerAccount(t)
	c := dialLobby(t, addr)
	userID, _, _ := c.login(ac, "******", "pilot")
	c.ask(0x6305, u16(1))
	res := call(t, rpcc, &lobbyrpc.RegisterProxyRequest{
		CurrentVersion: requiredVersion,
		UserID:         userID,
		LocalIP:        net.IPv4(192, 168, 0, 10),
		Port:           9000,
		UDPAddrs:       []string{"192.168.0.10:9000"},
	}).(*lobbyrpc.RegisterProxyResponse)
	if !res.Result {
		t.Fatalf("register proxy: %+v", res)
	}
	return app, c, ac, userID
}

func expectHeard(t *testing.T, c *testClient, line string) {
	t.Helper()
	want, err := japanese.ShiftJIS.NewEncoder().String(line)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.heard(); fmt.Sprint(got) != fmt.Sprint([]string{want}) {
		t.Errorf("heard %q, want %q", got, want)
	}
}

func TestLobbyTestBattle(t *testing.T) {
	app, c, ac, userID := lobby1Proxy(t)
	c.ask(0x640E, []byte{model.EntryAeug})
	expectHeard(t, c, "SERVER:接続テスト対戦開始")
	c.expect(0x6910, CategoryNotice)
	c.ask(0x6002, nil)

	rpcc := startLobbyRPC(t, app)
	res := call(t, rpcc, &lobbyrpc.BattleInfoRequest{SessionID: ac.SessionID}).(*lobbyrpc.BattleInfoResponse)
	if !res.Result || !res.IsTest || len(res.Users) != 1 || res.Users[0].UserID != userID {
		t.Errorf("battle info: %+v", res)
	}
}

func TestLobbyTestBattleStaleProxy(t *testing.T) {
	app, c, _, _ := lobby1Proxy(t)
	app.Locked(func(a *App) {
		for _, p := range a.users {
			p.proxyRegTime = time.Now().Add(-21 * time.Second)
		}
	})
	c.ask(0x640E, []byte{model.EntryAeug})
	expectHeard(t, c, "SERVER:UDPプロキシが登録されていません")
	for _, m := range c.queue {
		if m.Command == 0x6910 {
			t.Errorf("battle start notice with a proxy registered 21 s ago")
		}
	}
}
