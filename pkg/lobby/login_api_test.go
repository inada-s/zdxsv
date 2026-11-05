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
	dbtest.Use(t)
	oldConf := config.Conf
	config.Conf.Lobby.PublicAddr = "198.51.100.7:8200"
	t.Cleanup(func() { config.Conf = oldConf })
	ts := httptest.NewServer(&APIHandler{})
	t.Cleanup(ts.Close)

	registered, err := db.DefaultDB.RegisterAccount("198.51.100.8")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.PostForm(ts.URL+"/lbs/login", url.Values{"login_key": {registered.LoginKey}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var got login.APILoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	a, err := db.DefaultDB.GetAccountByLoginKey(registered.LoginKey)
	if err != nil {
		t.Fatal(err)
	}
	want := login.APILoginResponse{LoginKey: registered.LoginKey, SessionID: a.SessionID, LobbyAddr: "198.51.100.7:8200", LbsInfo: login.LbsInfo}
	if a.SessionID == "" || got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	for _, c := range []struct {
		method string
		form   url.Values
		code   int
	}{
		{http.MethodGet, nil, http.StatusMethodNotAllowed},
		{http.MethodPost, url.Values{}, http.StatusBadRequest},
		{http.MethodPost, url.Values{"login_key": {"short"}}, http.StatusForbidden},
		{http.MethodPost, url.Values{"login_key": {"unknownKey"}}, http.StatusForbidden},
	} {
		req, _ := http.NewRequest(c.method, ts.URL+"/lbs/login", strings.NewReader(c.form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.code {
			t.Errorf("%s %v: status %d, want %d", c.method, c.form, resp.StatusCode, c.code)
		}
	}
	if _, err := db.DefaultDB.GetAccountByLoginKey("unknownKey"); err == nil {
		t.Error("unknown key was registered")
	}
}
