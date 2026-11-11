package lobby

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"zdxsv/pkg/config"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	"zdxsv/pkg/login"
)

func TestAPILogin(t *testing.T) {
	dbtest.UseTestDB(t)
	oldConf := config.Conf
	config.Conf.Lobby.PublicAddr = "198.51.100.7:8200"
	t.Cleanup(func() { config.Conf = oldConf })
	post := func(method string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/lbs/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		(&APIHandler{}).ServeHTTP(w, req)
		return w
	}

	registered, err := db.DefaultDB.RegisterIdentityAccount("discord", "7777", "198.51.100.8")
	if err != nil {
		t.Fatal(err)
	}
	token, err := db.DefaultDB.IssueAuthToken("discord", "7777")
	if err != nil {
		t.Fatal(err)
	}
	resp := post(http.MethodPost, url.Values{"token": {token}})
	if resp.Code != 200 || resp.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", resp.Code, resp.Header().Get("Content-Type"))
	}
	var got login.APILoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if a, ok := login.Sessions.Use(got.SessionID, got.LoginKey); !ok || a != registered.LoginKey || got.LoginKey == registered.LoginKey {
		t.Fatalf("got %+v: account %q %v", got, a, ok)
	}
	want := login.APILoginResponse{LoginKey: got.LoginKey, SessionID: got.SessionID, LobbyAddr: "198.51.100.7:8200", LbsInfo: login.LbsInfo, Account: 1}
	if got.SessionID == "" || got.LoginKey == "" || got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	for _, c := range []struct {
		method string
		form   url.Values
		code   int
	}{
		{http.MethodGet, nil, http.StatusMethodNotAllowed},
		{http.MethodPost, url.Values{}, http.StatusBadRequest},
		{http.MethodPost, url.Values{"login_key": {registered.LoginKey}}, http.StatusForbidden},
		{http.MethodPost, url.Values{"token": {"unknown"}}, http.StatusForbidden},
	} {
		if code := post(c.method, c.form).Code; code != c.code {
			t.Errorf("%s %v: status %d, want %d", c.method, c.form, code, c.code)
		}
	}
}
