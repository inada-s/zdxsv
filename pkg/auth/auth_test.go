package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	"zdxsv/pkg/login"
)

type fakeProvider struct {
	*httptest.Server
	userID string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	f := &fakeProvider{}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		back := url.Values{"code": {"pcode-" + f.userID}, "state": {q.Get("state")}}
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+back.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("client_secret") != "secret" || !strings.HasPrefix(r.FormValue("code"), "pcode-") {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "at-" + strings.TrimPrefix(r.FormValue("code"), "pcode-")})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer at-")})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

type rig struct {
	t        *testing.T
	provider *fakeProvider
	auth     *Server
	site     *httptest.Server
	noFollow *http.Client
}

func newRig(t *testing.T) *rig {
	dbtest.Use(t)
	p := newFakeProvider(t)
	r := &rig{t: t, provider: p}
	r.auth = &Server{
		Provider: Provider{Name: "discord", AuthURL: p.URL + "/authorize", TokenURL: p.URL + "/token",
			UserURL: p.URL + "/user", Scope: "identify", ClientID: "cid", ClientSecret: "secret"},
		Store: db.DefaultDB,
	}
	mux := http.NewServeMux()
	mux.Handle("/auth/", r.auth)
	mux.HandleFunc("/lbs/login", login.HandleAPILogin)
	r.site = httptest.NewServer(mux)
	t.Cleanup(r.site.Close)
	r.auth.PublicURL = r.site.URL
	r.noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return r
}

const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk-test"

func challenge(v string) string {
	s := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

var buttonRe = regexp.MustCompile(`href="([^"]+)"`)

// signIn runs the browser part and returns the one-time code given to the emulator, or the
// status of the page that stopped it.
func (r *rig) signIn(discordID, carry string) (string, int) {
	r.provider.userID = discordID
	q := url.Values{"redirect_port": {"50123"}, "state": {"st"}, "code_challenge": {challenge(verifier)}, "lang": {"en"}}
	if carry != "" {
		q.Set("carry", carry)
	}
	resp, err := http.Get(r.site.URL + "/auth/start?" + q.Encode())
	if err != nil {
		r.t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := buttonRe.FindSubmatch(body)
	if resp.StatusCode != 200 || m == nil {
		r.t.Fatalf("start: %d %s", resp.StatusCode, body)
	}
	resp, err = r.noFollow.Get(html.UnescapeString(string(m[1])))
	if err != nil {
		r.t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = r.noFollow.Get(resp.Header.Get("Location"))
	if err != nil {
		r.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		return "", resp.StatusCode
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Host != "127.0.0.1:50123" || loc.Path != "/callback" || loc.Query().Get("state") != "st" {
		r.t.Fatalf("emulator redirect %s", loc)
	}
	return loc.Query().Get("code"), resp.StatusCode
}

func (r *rig) token(code, v string) (string, int) {
	resp, err := http.PostForm(r.site.URL+"/auth/token", url.Values{"code": {code}, "code_verifier": {v}})
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct{ Token string }
	json.NewDecoder(resp.Body).Decode(&got)
	return got.Token, resp.StatusCode
}

func (r *rig) lbsLogin(token string) (login.APILoginResponse, int) {
	return r.lbsLoginAs(token, "")
}

func (r *rig) lbsLoginAs(token, account string) (login.APILoginResponse, int) {
	v := url.Values{"token": {token}}
	if account != "" {
		v.Set("account", account)
	}
	resp, err := http.PostForm(r.site.URL+"/lbs/login", v)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var got login.APILoginResponse
	json.NewDecoder(resp.Body).Decode(&got)
	return got, resp.StatusCode
}

func TestSignInRegistersThenLogsIn(t *testing.T) {
	r := newRig(t)
	code, st := r.signIn("1111", "")
	if st != http.StatusFound {
		t.Fatal("sign in", st)
	}
	if _, st := r.token(code, verifier+"x"); st != http.StatusBadRequest {
		t.Fatal("wrong verifier accepted", st)
	}
	if _, st := r.token(code, verifier); st != http.StatusBadRequest {
		t.Fatal("code reused after a failed exchange", st)
	}
	code, _ = r.signIn("1111", "")
	token, st := r.token(code, verifier)
	if st != 200 || token == "" {
		t.Fatal("token", st)
	}
	if _, st := r.token(code, verifier); st != http.StatusBadRequest {
		t.Fatal("code reused", st)
	}
	key, err := db.DefaultDB.IdentityLoginKey("discord", "1111")
	if err != nil || key == "" {
		t.Fatal("identity", key, err)
	}

	first, st := r.lbsLogin(token)
	if st != 200 || first.LoginKey == "" || first.LoginKey == key {
		t.Fatalf("lbs login %d %+v, account key %s", st, first, key)
	}
	a, err := db.DefaultDB.GetAccountByLoginOrTempKey(first.LoginKey)
	if err != nil || a.LoginKey != key || a.SessionID != first.SessionID {
		t.Fatalf("temp key account %+v %v", a, err)
	}
	second, _ := r.lbsLogin(token)
	if second.LoginKey == first.LoginKey {
		t.Fatal("temp key not rotated")
	}
	if _, err := db.DefaultDB.GetAccountByLoginOrTempKey(first.LoginKey); err == nil {
		t.Fatal("old temp key still logs in")
	}

	code, _ = r.signIn("1111", "")
	token2, _ := r.token(code, verifier)
	if k, _ := db.DefaultDB.IdentityLoginKey("discord", "1111"); k != key {
		t.Fatal("second sign-in made a new account", k)
	}
	resp, err := http.PostForm(r.site.URL+"/auth/revoke", url.Values{"token": {token}})
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatal("revoke", err, resp.StatusCode)
	}
	if _, st := r.lbsLogin(token); st != http.StatusForbidden {
		t.Fatal("revoked token logs in", st)
	}
	if _, st := r.lbsLogin(token2); st != 200 {
		t.Fatal("other token revoked too", st)
	}
}

func TestCarryOverLinksExistingAccount(t *testing.T) {
	r := newRig(t)
	old, err := db.DefaultDB.RegisterAccount("198.51.100.8")
	if err != nil {
		t.Fatal(err)
	}
	carry := r.auth.IssueCarryCode(old.LoginKey)
	code, st := r.signIn("2222", strings.ToLower(carry))
	if st != http.StatusFound {
		t.Fatal("carry sign in", st)
	}
	token, _ := r.token(code, verifier)
	got, st := r.lbsLogin(token)
	if a, _ := db.DefaultDB.GetAccountByLoginOrTempKey(got.LoginKey); st != 200 || a == nil || a.LoginKey != old.LoginKey {
		t.Fatalf("carried login %d %+v", st, a)
	}
	if _, st := r.signIn("3333", carry); st != http.StatusForbidden {
		t.Fatal("carry code reused", st)
	}
	again := r.auth.IssueCarryCode(old.LoginKey)
	if _, st := r.signIn("4444", again); st != http.StatusForbidden {
		t.Fatal("linked account linked twice", st)
	}
	other, _ := db.DefaultDB.RegisterAccount("198.51.100.9")
	if _, st := r.signIn("2222", r.auth.IssueCarryCode(other.LoginKey)); st != http.StatusFound {
		t.Fatal("identity refused a second account", st)
	}
	if got, st := r.lbsLoginAs(token, "2"); st != 200 || mustAccount(t, got.LoginKey) != other.LoginKey {
		t.Fatalf("carried second account %d %+v", st, got)
	}
	if k, _ := db.DefaultDB.IdentityLoginKey("discord", "4444"); k != "" {
		t.Fatal("failed carry registered an account", k)
	}
}

func mustAccount(t *testing.T, gameKey string) string {
	a, err := db.DefaultDB.GetAccountByLoginOrTempKey(gameKey)
	if err != nil {
		t.Fatal("no account for", gameKey, err)
	}
	return a.LoginKey
}

type accountList struct {
	Accounts []struct {
		Account int
		Users   []string
	}
	Default, Max, Added int
}

func (r *rig) accounts(path, token string) (accountList, int) {
	resp, err := http.PostForm(r.site.URL+path, url.Values{"token": {token}})
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var got accountList
	json.NewDecoder(resp.Body).Decode(&got)
	return got, resp.StatusCode
}

func TestAccountsUpToMax(t *testing.T) {
	r := newRig(t)
	code, _ := r.signIn("5555", "")
	token, _ := r.token(code, verifier)
	list, st := r.accounts("/auth/accounts", token)
	if st != 200 || len(list.Accounts) != 1 || list.Default != 1 || list.Max != db.MaxIdentityAccounts {
		t.Fatalf("list %d %+v", st, list)
	}
	keys := map[string]int{}
	for n := 2; n <= db.MaxIdentityAccounts; n++ {
		list, st = r.accounts("/auth/accounts/add", token)
		if st != 200 || list.Added != n || len(list.Accounts) != n || list.Default != 1 {
			t.Fatalf("add %d: %d %+v", n, st, list)
		}
	}
	if _, st := r.accounts("/auth/accounts/add", token); st != http.StatusConflict {
		t.Fatal("account past the cap", st)
	}
	code, _ = r.signIn("5555", "")
	if _, st := r.token(code, verifier); st != 200 {
		t.Fatal("full identity cannot sign in", st)
	}
	for n := 1; n <= db.MaxIdentityAccounts; n++ {
		got, st := r.lbsLoginAs(token, strconv.Itoa(n))
		a, _ := db.DefaultDB.GetAccountByLoginOrTempKey(got.LoginKey)
		if st != 200 || a == nil {
			t.Fatalf("login account %d: %d", n, st)
		}
		keys[a.LoginKey] = n
	}
	if len(keys) != db.MaxIdentityAccounts {
		t.Fatal("accounts not distinct", keys)
	}
	first, _ := r.lbsLogin(token)
	if a, _ := db.DefaultDB.GetAccountByLoginOrTempKey(first.LoginKey); a == nil || keys[a.LoginKey] != 1 {
		t.Fatal("no account given: not the default one")
	}
	for _, bad := range []string{"0", "6", "x", "-1"} {
		if _, st := r.lbsLoginAs(token, bad); st != http.StatusForbidden {
			t.Fatal("account", bad, st)
		}
	}
	if _, st := r.accounts("/auth/accounts", "nope"); st != http.StatusForbidden {
		t.Fatal("unknown token listed", st)
	}

	code, _ = r.signIn("6666", "")
	other, _ := r.token(code, verifier)
	otherList, _ := r.accounts("/auth/accounts", other)
	if len(otherList.Accounts) != 1 {
		t.Fatal("identities share accounts", otherList)
	}
	key, _ := db.DefaultDB.IdentityLoginKey("discord", "6666")
	u, _ := db.DefaultDB.RegisterUser(key)
	if err := db.DefaultDB.AddBan(&db.Ban{Kind: db.BanUser, Value: u.UserID}); err != nil {
		t.Fatal(err)
	}
	if _, st := r.accounts("/auth/accounts/add", other); st != http.StatusForbidden {
		t.Fatal("banned identity added an account", st)
	}
}

func TestCarryOverMissesLock(t *testing.T) {
	r := newRig(t)
	a, _ := db.DefaultDB.RegisterAccount("198.51.100.8")
	for i := 0; i < carryMaxMisses; i++ {
		r.signIn("5555", "WRONGCOD")
	}
	good := r.auth.IssueCarryCode(a.LoginKey)
	if _, st := r.signIn("5555", good); st != http.StatusForbidden {
		t.Fatal("good code accepted after the miss limit", st)
	}
}

func TestStartRejectsBadRequests(t *testing.T) {
	r := newRig(t)
	ok := url.Values{"redirect_port": {"50123"}, "state": {"st"}, "code_challenge": {challenge(verifier)}}
	for _, c := range []struct{ k, v string }{
		{"redirect_port", "80"}, {"redirect_port", "x"}, {"state", ""}, {"code_challenge", "short"},
	} {
		q := url.Values{}
		for k, v := range ok {
			q[k] = v
		}
		q.Set(c.k, c.v)
		resp, err := http.Get(r.site.URL + "/auth/start?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s=%q: %d", c.k, c.v, resp.StatusCode)
		}
	}
	resp, _ := r.noFollow.Get(r.site.URL + "/auth/discord/callback?state=unknown&code=pcode-1")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Error("unknown flow", resp.StatusCode)
	}
}
