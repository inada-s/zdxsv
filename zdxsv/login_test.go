package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
)

// startLoginServer serves the login handlers the way newLoginHandler routes them.
func startLoginServer(t *testing.T) *httptest.Server {
	t.Helper()
	PrepareLoginPages()
	oldConf := Conf
	Conf.Lobby.PublicAddr = "198.51.100.7:8200"
	t.Cleanup(func() { Conf = oldConf })
	mux := http.NewServeMux()
	mux.HandleFunc("/CRS-top.jsp", HandleTopPage)
	mux.HandleFunc("/login", HandleLoginPage)
	mux.HandleFunc("/register", HandleRegisterPage)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// fetch returns the status, the content type and the body decoded from Shift_JIS.
func fetch(t *testing.T) func(*http.Response, error) (int, string, string) {
	return func(resp *http.Response, err error) (int, string, string) {
		t.Helper()
		return readPage(t, resp, err)
	}
}

func readPage(t *testing.T, resp *http.Response, err error) (int, string, string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := japanese.ShiftJIS.NewDecoder().Bytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
}

func postLogin(t *testing.T, ts *httptest.Server, key string) string {
	t.Helper()
	code, _, body := fetch(t)(http.PostForm(ts.URL+"/login", url.Values{"login_key": {key}}))
	if code != 200 {
		t.Fatalf("login status %d", code)
	}
	return body
}

var signupRe = regexp.MustCompile(`^<!--COMP-SIGNUP--><!--INPUT-IDS   ([0-9A-Za-z]{10})-->\n`)

func TestTopPage(t *testing.T) {
	ts := startLoginServer(t)
	code, ctype, body := fetch(t)(http.Get(ts.URL + "/CRS-top.jsp"))
	if code != 200 || ctype != "text/html; charset=cp932" {
		t.Fatalf("status %d, content type %q", code, ctype)
	}
	if !strings.Contains(body, "<html") && !strings.Contains(body, "<HTML") {
		t.Fatalf("top page is not html: %.200q", body)
	}
}

func TestRegisterThenLogin(t *testing.T) {
	UseTestDB(t)
	ts := startLoginServer(t)

	code, _, body := fetch(t)(http.Get(ts.URL + "/register"))
	if code != 200 {
		t.Fatalf("register status %d", code)
	}
	m := signupRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("register page has no login key: %.200q", body)
	}
	key := m[1]
	if !strings.Contains(body, "ユーザ作成が完了しました") {
		t.Fatalf("register page has no completion message: %q", body)
	}

	sessionOf := func(body string) string {
		t.Helper()
		m := regexp.MustCompile(`"OK",\s*"` + key + `",\s*"(\d{8})"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("login page has no session id: %q", body)
		}
		if _, ok := Sessions.Use(m[1], key); !ok {
			t.Fatalf("session %s not stored", m[1])
		}
		return m[1]
	}
	body = postLogin(t, ts, key)
	first := sessionOf(body)
	if !strings.Contains(body, `"198.51.100.7:8200"`) {
		t.Errorf("login page lacks the lobby address: %q", body)
	}

	// A second login issues a new session id and keeps the first one.
	if second := sessionOf(postLogin(t, ts, key)); second == first {
		t.Errorf("second login reused session id %s", first)
	}
	if _, ok := Sessions.Use(first, key); !ok {
		t.Error("second login ended the first session")
	}
}

func TestLoginUnknownKey(t *testing.T) {
	UseTestDB(t)
	ts := startLoginServer(t)

	for _, key := range []string{"", "short", "AbCdE12345", "ElevenChars"} {
		body := postLogin(t, ts, key)
		if !strings.Contains(body, "ログインに失敗しました") {
			t.Errorf("key %q: want the login failure page, got %q", key, body)
		}
		if key != "" {
			if _, err := DefaultDB.GetAccountByLoginKey(key); err == nil {
				t.Errorf("key %q was registered", key)
			}
		}
	}
}

func TestDBErrorPages(t *testing.T) {
	conn := UseTestDB(t)
	PrepareLoginPages()
	conn.Close()

	for _, tc := range []struct {
		name, method, target, want string
		h                          http.HandlerFunc
	}{
		{"register", "GET", "/register", "登録に失敗しました", HandleRegisterPage},
		{"login", "POST", "/login?login_key=AbCdE12345", "ログインに失敗しました", HandleLoginPage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("handler panicked: %v", r)
					}
				}()
				tc.h(rec, httptest.NewRequest(tc.method, tc.target, nil))
			}()
			_, _, body := fetch(t)(rec.Result(), nil)
			if !strings.Contains(body, tc.want) {
				t.Errorf("want %q, got %q", tc.want, body)
			}
			if strings.Contains(body, "COMP-SIGNUP") {
				t.Errorf("failure page carries a signup marker: %q", body)
			}
		})
	}
}

func TestSessionStore(t *testing.T) {
	s := NewSessionStore()
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }

	first, noTemp := s.Start("key1", false)
	second, temp := s.Start("key1", true)
	if first == second || noTemp != "" || !regexp.MustCompile(`^[1-9]{8}$`).MatchString(first) ||
		!regexp.MustCompile(`^[a-zA-Z0-9]{10}$`).MatchString(temp) {
		t.Fatalf("sessions %q %q, temporary keys %q %q", first, second, noTemp, temp)
	}
	for _, c := range []struct {
		id, key string
		ok      bool
	}{
		{first, "key1", true},
		{second, "key1", true},
		{second, temp, true},
		{first, temp, false},
		{first, "key2", false},
		{first, "", false},
		{"00000000", "key1", false},
	} {
		if got, ok := s.Use(c.id, c.key); ok != c.ok || (ok && got != "key1") {
			t.Errorf("Use(%s, %q) = %q, %v", c.id, c.key, got, ok)
		}
	}

	now = now.Add(SessionTTL - time.Minute)
	s.Touch(second)
	now = now.Add(2 * time.Minute)
	if _, ok := s.Use(first, "key1"); ok {
		t.Error("a session unused for SessionTTL opens")
	}
	if _, ok := s.Use(second, temp); !ok {
		t.Error("a touched session ended")
	}

	other, _ := s.Start("key2", false)
	var ids []string
	for i := 0; i < MaxSessionsPerAccount; i++ {
		now = now.Add(time.Second)
		id, _ := s.Start("key1", false)
		ids = append(ids, id)
	}
	if _, ok := s.Use(second, "key1"); ok {
		t.Error("the least recently used session outlived the account's limit")
	}
	for _, id := range ids {
		if _, ok := s.Use(id, "key1"); !ok {
			t.Errorf("session %s of the latest %d ended", id, MaxSessionsPerAccount)
		}
	}
	if _, ok := s.Use(other, "key2"); !ok {
		t.Error("another account's session ended")
	}
	if len(s.sessions) != MaxSessionsPerAccount+1 {
		t.Errorf("%d sessions kept", len(s.sessions))
	}
}

func TestLoginHandlerAssetsAndHealth(t *testing.T) {
	srv := httptest.NewServer(newLoginHandler())
	t.Cleanup(srv.Close)

	get := func(path string, header map[string]string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest("GET", srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, body
	}

	res, body := get("/health", nil)
	if res.StatusCode != 200 || string(body) != "works\n" {
		t.Errorf("/health: %d %q", res.StatusCode, body)
	}

	for _, c := range []struct{ name, contentType string }{
		{"assets/login.png", "image/png"},
		{"assets/checkfile", ""},
	} {
		want, err := Asset(c.name)
		if err != nil {
			t.Fatal(err)
		}
		res, body := get("/"+c.name, nil)
		if res.StatusCode != 200 || !bytes.Equal(body, want) {
			t.Errorf("%s: %d, %d bytes, want %d", c.name, res.StatusCode, len(body), len(want))
		}
		if c.contentType != "" && res.Header.Get("Content-Type") != c.contentType {
			t.Errorf("%s: Content-Type %q", c.name, res.Header.Get("Content-Type"))
		}
		if cc := res.Header.Get("Cache-Control"); cc != "max-age=60, public" {
			t.Errorf("%s: Cache-Control %q", c.name, cc)
		}
		modified, err := time.Parse(http.TimeFormat, res.Header.Get("Last-Modified"))
		if err != nil || modified.After(time.Now()) {
			t.Errorf("%s: Last-Modified %q", c.name, res.Header.Get("Last-Modified"))
		}
		if _, err := time.Parse(http.TimeFormat, res.Header.Get("Expires")); err != nil {
			t.Errorf("%s: Expires %q", c.name, res.Header.Get("Expires"))
		}

		// Any valid If-Modified-Since is answered from the client's cache.
		res, body = get("/"+c.name, map[string]string{"If-Modified-Since": modified.Format(http.TimeFormat)})
		if res.StatusCode != http.StatusNotModified || len(body) != 0 {
			t.Errorf("%s revalidated: %d, %d bytes", c.name, res.StatusCode, len(body))
		}
	}

	if res, _ := get("/assets/missing.png", nil); res.StatusCode != 404 {
		t.Errorf("missing asset: %d", res.StatusCode)
	}
	if res, _ := get("/unknown", nil); res.StatusCode != 404 {
		t.Errorf("unknown path: %d", res.StatusCode)
	}
}

func TestAPILogin(t *testing.T) {
	UseTestDB(t)
	oldConf := Conf
	Conf.Lobby.PublicAddr = "198.51.100.7:8200"
	t.Cleanup(func() { Conf = oldConf })
	post := func(method string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/lbs/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		(&APIHandler{}).ServeHTTP(w, req)
		return w
	}

	registered, err := DefaultDB.RegisterIdentityAccount("discord", "7777", "198.51.100.8")
	if err != nil {
		t.Fatal(err)
	}
	token, err := DefaultDB.IssueAuthToken("discord", "7777")
	if err != nil {
		t.Fatal(err)
	}
	resp := post(http.MethodPost, url.Values{"token": {token}})
	if resp.Code != 200 || resp.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", resp.Code, resp.Header().Get("Content-Type"))
	}
	var got APILoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if a, ok := Sessions.Use(got.SessionID, got.LoginKey); !ok || a != registered.LoginKey || got.LoginKey == registered.LoginKey {
		t.Fatalf("got %+v: account %q %v", got, a, ok)
	}
	want := APILoginResponse{LoginKey: got.LoginKey, SessionID: got.SessionID, LobbyAddr: "198.51.100.7:8200", LbsInfo: LbsInfo, Account: 1}
	if got.SessionID == "" || got.LoginKey == "" || got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for _, c := range []struct{ form, want string }{
		{"", gameCodeZGVSZ},
		{gameCodeAEUG, gameCodeAEUG},
		{gameCodeZGVSZ, gameCodeZGVSZ},
		{"99999999", gameCodeZGVSZ},
	} {
		var r APILoginResponse
		if err := json.NewDecoder(post(http.MethodPost, url.Values{"token": {token}, "game": {c.form}}).Body).Decode(&r); err != nil {
			t.Fatal(err)
		}
		if g := Sessions.Game(r.SessionID); g != c.want {
			t.Errorf("game %q: session game %q, want %q", c.form, g, c.want)
		}
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
