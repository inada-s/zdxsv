package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestLoginPageGameCode(t *testing.T) {
	UseTestDB(t)
	PrepareLoginPages()
	a, err := DefaultDB.RegisterAccount("192.0.2.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for code, title := range map[string]string{gameCodeZGVSZ: "ガンダムvs.Zガンダム", gameCodeAEUG: "エゥーゴvs.ティターンズ"} {
		mux := http.NewServeMux()
		mux.Handle("/"+code+"/", http.StripPrefix("/"+code, withGameCode(code, newLoginHandler())))
		ts := httptest.NewServer(mux)
		_, _, top := fetch(t)(http.Get(ts.URL + "/" + code + "/CRS-top.jsp"))
		if !strings.Contains(top, title+" エミュ鯖") {
			t.Errorf("%s: top page lacks %s: %.300q", code, title, top)
		}
		_, _, body := fetch(t)(http.PostForm(ts.URL+"/"+code+"/login", url.Values{"login_key": {a.LoginKey}}))
		ts.Close()
		for _, want := range []string{"https://www01.kddi-mmbb.jp/" + code + "/login", "https://www01.kddi-mmbb.jp/" + code + "/CRS-top.jsp"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: login page lacks %s: %.300q", code, want, body)
			}
		}
	}
}

// gameLogin logs an account in on the login page of game code, as that game's browser does.
func gameLogin(t *testing.T, code string) *testLogin {
	t.Helper()
	PrepareLoginPages()
	a, err := DefaultDB.RegisterAccount("192.0.2.1:1")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/"+code+"/", http.StripPrefix("/"+code, withGameCode(code, newLoginHandler())))
	ts := httptest.NewServer(mux)
	defer ts.Close()
	_, _, body := fetch(t)(http.PostForm(ts.URL+"/"+code+"/login", url.Values{"login_key": {a.LoginKey}}))
	m := regexp.MustCompile(`"OK",\s*"` + a.LoginKey + `",\s*"(\d{8})"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("login page has no session id: %.300q", body)
	}
	return &testLogin{LoginKey: a.LoginKey, SessionID: m[1]}
}

// The two games share the server but not the lobbies: a client sees only the users
// and battles of its own game.
func TestGamesKeepApart(t *testing.T) {
	UseTestDB(t)
	addr := startLobby(t, NewMcs())
	const lobbyID = 4
	users := func(c *testClient) uint16 {
		r := c.ask(0x6303, u16(lobbyID)).Reader()
		r.Read16()
		return r.Read16()
	}
	clients := map[string][2]*testClient{}
	for _, code := range []string{gameCodeZGVSZ, gameCodeAEUG} {
		in, look := dialLobby(t, addr), dialLobby(t, addr)
		in.login(gameLogin(t, code), "******", "in"+code)
		look.login(gameLogin(t, code), "******", "look"+code)
		clients[code] = [2]*testClient{in, look}
	}

	if a := clients[gameCodeZGVSZ][0].ask(0x6305, u16(lobbyID)); a.Status != StatusSuccess {
		t.Fatalf("EnterLobby status %x", a.Status)
	}
	if got := users(clients[gameCodeZGVSZ][1]); got != 1 {
		t.Errorf("SLPS-25419 sees %d users in its lobby, want 1", got)
	}
	if got := users(clients[gameCodeAEUG][1]); got != 0 {
		t.Errorf("SLPS-25305 sees %d users of the other game, want 0", got)
	}

	if a := clients[gameCodeAEUG][0].ask(0x6305, u16(lobbyID)); a.Status != StatusSuccess {
		t.Fatalf("EnterLobby status %x", a.Status)
	}
	for code, c := range clients {
		if got := users(c[1]); got != 1 {
			t.Errorf("%s sees %d users in lobby %d, want 1", code, got, lobbyID)
		}
	}
}
