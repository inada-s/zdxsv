package lobby

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"zdxsv/pkg/battle"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	"zdxsv/pkg/lobby/lobbyapi"
	"zdxsv/pkg/lobby/model"
	"zdxsv/pkg/message"
)

// zproxyAPI is the lobby API as zproxy reaches it: through the reverse proxy on the lobby
// host, from ip.
type zproxyAPI struct {
	h  *APIHandler
	ip string
}

func newZproxyAPI(app *App) *zproxyAPI {
	return &zproxyAPI{h: &APIHandler{RegisterProxy: app.RegisterProxy, BattleInfo: app.BattleInfo}, ip: "127.0.0.1"}
}

func (z *zproxyAPI) call(t *testing.T, path string, req, res any) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("X-Forwarded-For", z.ip)
	w := httptest.NewRecorder()
	z.h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), res); err != nil {
		t.Fatal(err)
	}
}

func (z *zproxyAPI) register(t *testing.T, req *lobbyapi.RegisterProxyRequest) *lobbyapi.RegisterProxyResponse {
	t.Helper()
	res := new(lobbyapi.RegisterProxyResponse)
	z.call(t, lobbyapi.RegisterProxyPath, req, res)
	return res
}

func (z *zproxyAPI) battleInfo(t *testing.T, sessionID string) *lobbyapi.BattleInfoResponse {
	t.Helper()
	res := new(lobbyapi.BattleInfoResponse)
	z.call(t, lobbyapi.BattleInfoPath, &lobbyapi.BattleInfoRequest{SessionID: sessionID}, res)
	return res
}

func findUser(users []lobbyapi.User, userID string) *lobbyapi.User {
	for i := range users {
		if users[i].UserID == userID {
			return &users[i]
		}
	}
	return nil
}

// TestZproxyAPIFlow is what zproxy does: register while its user is in a
// lobby, then ask for the battle once the user went to the battle server.
func TestZproxyAPIFlow(t *testing.T) {
	conn := dbtest.Use(t)
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('console', 2, '1v1', '', '', '')`); err != nil {
		t.Fatal(err)
	}
	addr, app := startLobbyApp(t, battle.NewLogic())
	api := newZproxyAPI(app)

	type player struct {
		c      *testClient
		ac     *testLogin
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

	good := lobbyapi.RegisterProxyRequest{
		CurrentVersion: requiredVersion,
		UserID:         p0.userID,
		LocalIP:        net.IPv4(192, 168, 0, 10),
		Port:           9000,
		UDPAddrs:       []string{"192.168.0.10:9000", "203.0.113.1:9000"},
	}
	rejected := []struct {
		name string
		edit func(*lobbyapi.RegisterProxyRequest)
		msg  string
	}{
		{"old version", func(r *lobbyapi.RegisterProxyRequest) { r.CurrentVersion = requiredVersion - 1 }, "プロキシソフトのバージョンが古いです"},
		{"no UDP address", func(r *lobbyapi.RegisterProxyRequest) { r.UDPAddrs = nil }, "UDPアドレスが取得できていません"},
		{"no local IP", func(r *lobbyapi.RegisterProxyRequest) { r.LocalIP = nil }, "無効なローカルIPアドレスです"},
		{"IPv6 local IP", func(r *lobbyapi.RegisterProxyRequest) { r.LocalIP = net.ParseIP("fe80::1") }, "無効なローカルIPアドレスです"},
		{"user not in lobby", func(r *lobbyapi.RegisterProxyRequest) { r.UserID = "NOUSER" }, "ロビーにユーザが見つかりません"},
	}
	for _, c := range rejected {
		req := good
		c.edit(&req)
		res := api.register(t, &req)
		if res.Result || res.Message != c.msg || res.SessionID != "" {
			t.Errorf("%s: %+v", c.name, res)
		}
	}

	res := api.register(t, &good)
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
	if res := api.register(t, &auto); !res.Result || (res.UserID != p0.userID && res.UserID != p1.userID) {
		t.Errorf("register _AUTO_: %+v", res)
	}
	far := newZproxyAPI(app)
	for _, c := range []struct{ ip, userID, msg string }{
		{"203.0.113.9", "_AUTO_", "ロビーにユーザが見つかりません"},
		{"203.0.113.9", p0.userID, "ロビーにユーザが見つかりません"},
		{"2001:db8::1", "_AUTO_", "無効なグローバルIPアドレスです"},
	} {
		far.ip = c.ip
		req := good
		req.UserID = c.userID
		if res := far.register(t, &req); res.Result || res.Message != c.msg {
			t.Errorf("register %s from %s: %+v", c.userID, c.ip, res)
		}
	}

	st := app.Status()
	if len(st.LobbyUsers) != 2 || len(st.Battles) != 0 {
		t.Errorf("status in lobby: %+v", st)
	}
	if res := api.battleInfo(t, p0.ac.SessionID); res.Result {
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
		res := api.battleInfo(t, p.ac.SessionID)
		if !res.Result || !res.BattleIP.Equal(net.IPv4(127, 0, 0, 2)) || res.Port != 8210 || res.IsTest || len(res.Users) != 2 {
			t.Fatalf("battle info: %+v", res)
		}
		if u := findUser(res.Users, p0.userID); u == nil || u.Name != "pilot0" {
			t.Errorf("battle user %+v", u)
		}
	}
	for _, sid := range []string{"", "999999999"} {
		if res := api.battleInfo(t, sid); res.Result {
			t.Errorf("battle info for session %q: %+v", sid, res)
		}
	}

	st = app.Status()
	if len(st.LobbyUsers) != 0 || len(st.Battles) != 1 {
		t.Fatalf("status in battle: %+v", st)
	}
	if b := st.Battles[0]; len(b.Users) != 2 || !reflect.DeepEqual(b.AeugIDs, []string{p0.userID}) || !reflect.DeepEqual(b.TitansIDs, []string{p1.userID}) {
		t.Errorf("status battle %+v", b)
	}

	// The user left the lobby for the battle.
	if res := api.register(t, &good); res.Result {
		t.Errorf("register after GotoBattle: %+v", res)
	}
}

func TestStatusPlatform(t *testing.T) {
	app := NewApp()
	ps2 := &AppPeer{Platform: model.PlatformConsole}
	ps2.UserID = "AAAAAA"
	emu := &AppPeer{Platform: model.PlatformEmu}
	emu.UserID = "BBBBBB"
	app.users["SAAAAAA"] = ps2
	app.users["SBBBBBB"] = emu
	b := model.NewBattle(model.PlatformEmu, 0)
	b.Users = append(b.Users, model.User{User: db.User{UserID: "CCCCCC", SessionID: "SCCCCCC"}})
	app.battles["SCCCCCC"] = b

	res := app.status()
	got := map[string]string{}
	for _, u := range res.LobbyUsers {
		got[u.UserID] = u.Platform
	}
	if got["AAAAAA"] != model.PlatformConsole || got["BBBBBB"] != model.PlatformEmu {
		t.Errorf("lobby platforms %v", got)
	}
	if len(res.Battles) != 1 || res.Battles[0].Platform != model.PlatformEmu ||
		len(res.Battles[0].Users) != 1 || res.Battles[0].Users[0].Platform != model.PlatformEmu {
		t.Errorf("battles %+v", res.Battles)
	}
}
