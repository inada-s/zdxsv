package login

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"zdxsv/pkg/config"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"

	"golang.org/x/text/encoding/japanese"
)

// startLoginServer serves the login handlers the way src/zdxsv mainLogin routes them.
func startLoginServer(t *testing.T) *httptest.Server {
	t.Helper()
	Prepare()
	oldConf := config.Conf
	config.Conf.Lobby.PublicAddr = "198.51.100.7:8200"
	t.Cleanup(func() { config.Conf = oldConf })
	mux := http.NewServeMux()
	mux.HandleFunc("/CRS-top.jsp", HandleTopPage)
	mux.HandleFunc("/login", HandleLoginPage)
	mux.HandleFunc("/api/login", HandleAPILogin)
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
	dbtest.Use(t)
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

	body = postLogin(t, ts, key)
	a, err := db.DefaultDB.GetAccountByLoginKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionID == "" {
		t.Fatal("login did not store a session id")
	}
	for _, want := range []string{`"` + key + `"`, `"` + a.SessionID + `"`, `"198.51.100.7:8200"`} {
		if !strings.Contains(body, want) {
			t.Errorf("login page lacks %s: %q", want, body)
		}
	}

	// A second login issues a new session id.
	body = postLogin(t, ts, key)
	b, err := db.DefaultDB.GetAccountByLoginKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"`+b.SessionID+`"`) {
		t.Errorf("second login page lacks session id %s", b.SessionID)
	}
}

func TestAPILogin(t *testing.T) {
	dbtest.Use(t)
	ts := startLoginServer(t)

	resp, err := http.PostForm(ts.URL+"/api/login", url.Values{"login_key": {"apiKey0001"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var got apiLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	a, err := db.DefaultDB.GetAccountByLoginKey("apiKey0001")
	if err != nil {
		t.Fatal(err)
	}
	want := apiLoginResponse{"apiKey0001", a.SessionID, "198.51.100.7:8200", LbsInfo}
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
	} {
		req, _ := http.NewRequest(c.method, ts.URL+"/api/login", strings.NewReader(c.form.Encode()))
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
}

func TestLoginUnknownKey(t *testing.T) {
	dbtest.Use(t)
	ts := startLoginServer(t)

	// A 10 char key from another server is registered on first use.
	const foreign = "AbCdE12345"
	body := postLogin(t, ts, foreign)
	a, err := db.DefaultDB.GetAccountByLoginKey(foreign)
	if err != nil {
		t.Fatalf("foreign key not registered: %v", err)
	}
	if !strings.Contains(body, `"`+a.SessionID+`"`) {
		t.Errorf("login page lacks session id %s: %q", a.SessionID, body)
	}

	for _, key := range []string{"", "short", "ElevenChars"} {
		body := postLogin(t, ts, key)
		if !strings.Contains(body, "ログインに失敗しました") {
			t.Errorf("key %q: want the login failure page, got %q", key, body)
		}
		if key != "" {
			if _, err := db.DefaultDB.GetAccountByLoginKey(key); err == nil {
				t.Errorf("key %q was registered", key)
			}
		}
	}
}

func TestDBErrorPages(t *testing.T) {
	conn := dbtest.Use(t)
	Prepare()
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
