package lobby

import (
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	"zdxsv/pkg/lobby/lobbyrpc"
	"zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"

	"github.com/valyala/gorpc"
)

// startLobbyRPC runs the lobby RPC server zproxy talks to and returns a client of it.
func startLobbyRPC(t *testing.T, app *App) *gorpc.Client {
	t.Helper()
	s := newLobbyRPCServer(app, "127.0.0.1:0")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	c := gorpc.NewTCPClient(s.Listener.ListenAddr().String())
	c.Start()
	t.Cleanup(c.Stop)
	return c
}

func call(t *testing.T, c *gorpc.Client, req interface{}) interface{} {
	t.Helper()
	res, err := c.CallTimeout(req, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func findUser(users []lobbyrpc.User, userID string) *lobbyrpc.User {
	for i := range users {
		if users[i].UserID == userID {
			return &users[i]
		}
	}
	return nil
}

// TestLobbyRPCProxyFlow is what zproxy does: register while its user is in a
// lobby, then ask for the battle once the user went to the battle server.
func TestLobbyRPCProxyFlow(t *testing.T) {
	dbtest.Use(t)
	addr, app := startLobbyApp(t, battlerpc.NewBattleRpc())
	rpcc := startLobbyRPC(t, app)

	type player struct {
		c      *testClient
		ac     *db.Account
		userID string
	}
	sides := []byte{byte(model.EntryAeug), byte(model.EntryTitans)}
	var players []*player
	for i := range sides {
		p := &player{ac: registerAccount(t), c: dialLobby(t, addr)}
		p.userID, _, _ = p.c.login(p.ac, "******", fmt.Sprintf("pilot%d", i))
		if a := p.c.ask(0x6305, u16(2)); a.Status != message.StatusSuccess {
			t.Fatalf("EnterLobby status %x", a.Status)
		}
		players = append(players, p)
	}
	p0, p1 := players[0], players[1]

	good := lobbyrpc.RegisterProxyRequest{
		CurrentVersion: requiredVersion,
		UserID:         p0.userID,
		LocalIP:        net.IPv4(192, 168, 0, 10),
		Port:           9000,
		UDPAddrs:       []string{"192.168.0.10:9000", "203.0.113.1:9000"},
	}
	rejected := []struct {
		name string
		edit func(*lobbyrpc.RegisterProxyRequest)
		msg  string
	}{
		{"old version", func(r *lobbyrpc.RegisterProxyRequest) { r.CurrentVersion = requiredVersion - 1 }, "プロキシソフトのバージョンが古いです"},
		{"no UDP address", func(r *lobbyrpc.RegisterProxyRequest) { r.UDPAddrs = nil }, "UDPアドレスが取得できていません"},
		{"no local IP", func(r *lobbyrpc.RegisterProxyRequest) { r.LocalIP = nil }, "無効なローカルIPアドレスです"},
		{"IPv6 local IP", func(r *lobbyrpc.RegisterProxyRequest) { r.LocalIP = net.ParseIP("fe80::1") }, "無効なローカルIPアドレスです"},
		{"user not in lobby", func(r *lobbyrpc.RegisterProxyRequest) { r.UserID = "NOUSER" }, "ロビーにユーザが見つかりません"},
	}
	for _, c := range rejected {
		req := good
		c.edit(&req)
		res := call(t, rpcc, &req).(*lobbyrpc.RegisterProxyResponse)
		if res.Result || res.Message != c.msg || res.SessionID != "" {
			t.Errorf("%s: %+v", c.name, res)
		}
	}

	res := call(t, rpcc, &good).(*lobbyrpc.RegisterProxyResponse)
	if !res.Result || res.UserID != p0.userID || res.SessionID != p0.ac.SessionID || len(res.LobbyUsers) != 2 {
		t.Fatalf("register: %+v", res)
	}
	if u := findUser(res.LobbyUsers, p0.userID); u == nil || !u.UDP || !reflect.DeepEqual(u.UDPAddrs, good.UDPAddrs) || u.Name != "pilot0" {
		t.Errorf("registered user %+v", u)
	}
	if u := findUser(res.LobbyUsers, p1.userID); u == nil || u.UDP || len(u.UDPAddrs) != 0 {
		t.Errorf("unregistered user %+v", u)
	}
	// From the lobby host, _AUTO_ takes any user with the same IP.
	auto := good
	auto.UserID = "_AUTO_"
	if res := call(t, rpcc, &auto).(*lobbyrpc.RegisterProxyResponse); !res.Result || (res.UserID != p0.userID && res.UserID != p1.userID) {
		t.Errorf("register _AUTO_: %+v", res)
	}

	st := call(t, rpcc, &lobbyrpc.StatusRequest{}).(*lobbyrpc.StatusResponse)
	if len(st.LobbyUsers) != 2 || len(st.Battles) != 0 {
		t.Errorf("status in lobby: %+v", st)
	}
	if res := call(t, rpcc, &lobbyrpc.BattleInfoRequest{SessionID: p0.ac.SessionID}).(*lobbyrpc.BattleInfoResponse); res.Result {
		t.Errorf("battle info before the battle: %+v", res)
	}

	for i, p := range players {
		p.c.ask(0x640E, []byte{sides[i]})
	}
	for _, p := range players {
		p.c.expect(0x6910, message.CategoryNotice)
	}
	for _, p := range players {
		p.c.ask(0x6002, nil)
	}

	for _, p := range players {
		res := call(t, rpcc, &lobbyrpc.BattleInfoRequest{SessionID: p.ac.SessionID}).(*lobbyrpc.BattleInfoResponse)
		if !res.Result || !res.BattleIP.Equal(net.IPv4(127, 0, 0, 2)) || res.Port != 8210 || res.IsTest || len(res.Users) != 2 {
			t.Fatalf("battle info: %+v", res)
		}
		if u := findUser(res.Users, p0.userID); u == nil || u.Name != "pilot0" {
			t.Errorf("battle user %+v", u)
		}
	}
	for _, sid := range []string{"", "999999999"} {
		if res := call(t, rpcc, &lobbyrpc.BattleInfoRequest{SessionID: sid}).(*lobbyrpc.BattleInfoResponse); res.Result {
			t.Errorf("battle info for session %q: %+v", sid, res)
		}
	}

	st = call(t, rpcc, &lobbyrpc.StatusRequest{}).(*lobbyrpc.StatusResponse)
	if len(st.LobbyUsers) != 0 || len(st.Battles) != 1 {
		t.Fatalf("status in battle: %+v", st)
	}
	if b := st.Battles[0]; len(b.Users) != 2 || !reflect.DeepEqual(b.AeugIDs, []string{p0.userID}) || !reflect.DeepEqual(b.TitansIDs, []string{p1.userID}) {
		t.Errorf("status battle %+v", b)
	}

	// The user left the lobby for the battle.
	if res := call(t, rpcc, &good).(*lobbyrpc.RegisterProxyResponse); res.Result {
		t.Errorf("register after GotoBattle: %+v", res)
	}
}
