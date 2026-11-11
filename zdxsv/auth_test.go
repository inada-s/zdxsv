package main

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
	"time"
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

type authRig struct {
	t        *testing.T
	provider *fakeProvider
	auth     *AuthServer
	site     *httptest.Server
	noFollow *http.Client
}

func newAuthRig(t *testing.T) *authRig {
	UseTestDB(t)
	p := newFakeProvider(t)
	r := &authRig{t: t, provider: p}
	r.auth = &AuthServer{
		Provider: Provider{Name: "discord", AuthURL: p.URL + "/authorize", TokenURL: p.URL + "/token",
			UserURL: p.URL + "/user", Scope: "identify", ClientID: "cid", ClientSecret: "secret"},
		Store: DefaultDB,
	}
	mux := http.NewServeMux()
	mux.Handle("/auth/", r.auth)
	mux.HandleFunc("/lbs/login", HandleAPILogin)
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
func (r *authRig) signIn(discordID string) (string, int) {
	r.provider.userID = discordID
	q := url.Values{"redirect_port": {"50123"}, "state": {"st"}, "code_challenge": {challenge(verifier)}, "lang": {"en"}}
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

// tokenFor signs in and returns the emulator's token.
func (r *authRig) tokenFor(discordID string) string {
	code, st := r.signIn(discordID)
	if st != http.StatusFound {
		r.t.Fatal("sign in", st)
	}
	token, st := r.token(code, verifier)
	if st != 200 || token == "" {
		r.t.Fatal("token", st)
	}
	return token
}

func (r *authRig) token(code, v string) (string, int) {
	resp, err := http.PostForm(r.site.URL+"/auth/token", url.Values{"code": {code}, "code_verifier": {v}})
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct{ Token string }
	json.NewDecoder(resp.Body).Decode(&got)
	return got.Token, resp.StatusCode
}

func (r *authRig) lbsLogin(token string) (APILoginResponse, int) {
	return r.lbsLoginAs(token, "")
}

func (r *authRig) lbsLoginAs(token, account string) (APILoginResponse, int) {
	v := url.Values{"token": {token}}
	if account != "" {
		v.Set("account", account)
	}
	resp, err := http.PostForm(r.site.URL+"/lbs/login", v)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var got APILoginResponse
	json.NewDecoder(resp.Body).Decode(&got)
	return got, resp.StatusCode
}

func TestSignInCreatesNoAccount(t *testing.T) {
	r := newAuthRig(t)
	code, st := r.signIn("1111")
	if st != http.StatusFound {
		t.Fatal("sign in", st)
	}
	if _, st := r.token(code, verifier+"x"); st != http.StatusBadRequest {
		t.Fatal("wrong verifier accepted", st)
	}
	if _, st := r.token(code, verifier); st != http.StatusBadRequest {
		t.Fatal("code reused after a failed exchange", st)
	}
	code, _ = r.signIn("1111")
	token, st := r.token(code, verifier)
	if st != 200 || token == "" {
		t.Fatal("token", st)
	}
	if _, st := r.token(code, verifier); st != http.StatusBadRequest {
		t.Fatal("code reused", st)
	}
	if key, err := DefaultDB.IdentityLoginKey("discord", "1111"); err != nil || key != "" {
		t.Fatal("sign-in made an account", key, err)
	}
	if list, st := r.accounts("/auth/accounts", token); st != 200 || len(list.Accounts) != 0 || list.Default != 0 {
		t.Fatalf("list %d %+v", st, list)
	}
	made, st := r.lbsLogin(token)
	key, _ := DefaultDB.IdentityLoginKey("discord", "1111")
	if st != 200 || made.Account != 1 || key == "" || mustAccount(t, made) != key {
		t.Fatalf("first login of an identity with no account %d %+v", st, made)
	}
	if list, st := r.accounts("/auth/accounts", token); st != 200 || len(list.Accounts) != 1 || list.Default != 1 {
		t.Fatalf("list after the new account %d %+v", st, list)
	}
	first, st := r.lbsLogin(token)
	if st != 200 || first.LoginKey == "" || first.LoginKey == key {
		t.Fatalf("lbs login %d %+v, account key %s", st, first, key)
	}
	if got := sessionAccount(first); got != key {
		t.Fatalf("temp key account %q", got)
	}
	second, _ := r.lbsLogin(token)
	if second.LoginKey == first.LoginKey || second.SessionID == first.SessionID {
		t.Fatal("second login shares the first one's session", first, second)
	}
	if mustAccount(t, first) != key || mustAccount(t, second) != key {
		t.Fatal("a login ended the other session of the account")
	}
	if _, ok := Sessions.Use(second.SessionID, first.LoginKey); ok {
		t.Fatal("temp key logs in to another session")
	}

	token2 := r.tokenFor("1111")
	if got, st := r.lbsLogin(token2); st != 200 || mustAccount(t, got) != key {
		t.Fatal("second sign-in does not reach the account", st)
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

func (r *authRig) carry(token, code string) (accountList, int) {
	resp, err := http.PostForm(r.site.URL+"/auth/accounts/carry", url.Values{"token": {token}, "code": {code}})
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var got accountList
	json.NewDecoder(resp.Body).Decode(&got)
	return got, resp.StatusCode
}

func TestCarryOverLinksExistingAccount(t *testing.T) {
	r := newAuthRig(t)
	old, err := DefaultDB.RegisterAccount("198.51.100.8")
	if err != nil {
		t.Fatal(err)
	}
	token := r.tokenFor("2222")
	code := r.auth.IssueCarryCode(old.LoginKey)
	if list, st := r.carry(token, " "+strings.ToLower(code)+" "); st != 200 || list.Added != 1 || list.Default != 1 {
		t.Fatalf("carry %d %+v", st, list)
	}
	got, st := r.lbsLogin(token)
	if a := sessionAccount(got); st != 200 || a != old.LoginKey {
		t.Fatalf("carried login %d %q", st, a)
	}
	if _, st := r.carry(token, code); st != http.StatusBadRequest {
		t.Fatal("carry code reused", st)
	}
	if _, st := r.carry(r.tokenFor("4444"), r.auth.IssueCarryCode(old.LoginKey)); st != http.StatusUnprocessableEntity {
		t.Fatal("linked account linked twice", st)
	}
	other, _ := DefaultDB.RegisterAccount("198.51.100.9")
	if list, st := r.carry(token, r.auth.IssueCarryCode(other.LoginKey)); st != 200 || list.Added != 2 {
		t.Fatalf("identity refused a second account %d %+v", st, list)
	}
	if got, st := r.lbsLoginAs(token, "2"); st != 200 || mustAccount(t, got) != other.LoginKey {
		t.Fatalf("carried second account %d %+v", st, got)
	}
	if _, st := r.carry("nope", r.auth.IssueCarryCode(other.LoginKey)); st != http.StatusForbidden {
		t.Fatal("unknown token carried", st)
	}
}

func mustAccount(t *testing.T, res APILoginResponse) string {
	a := sessionAccount(res)
	if a == "" {
		t.Fatal("no account for", res)
	}
	return a
}

// sessionAccount is the login key of the session a login answered, "" when none.
func sessionAccount(res APILoginResponse) string {
	a, _ := Sessions.Use(res.SessionID, res.LoginKey)
	return a
}

type accountList struct {
	Accounts []struct {
		Account int
		Users   []string
		Carried bool
	}
	Default    int
	MaxNew     int `json:"max_new"`
	MaxCarried int `json:"max_carried"`
	Added      int
}

func (r *authRig) accounts(path, token string) (accountList, int) {
	resp, err := http.PostForm(r.site.URL+path, url.Values{"token": {token}})
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var got accountList
	json.NewDecoder(resp.Body).Decode(&got)
	return got, resp.StatusCode
}

func TestAccountLimits(t *testing.T) {
	r := newAuthRig(t)
	token := r.tokenFor("5555")
	for n := 1; n <= MaxIdentityNewAccounts; n++ {
		if got, st := r.lbsLoginAs(token, "new"); st != 200 || got.Account != n {
			t.Fatalf("new account %d: %d %+v", n, st, got)
		}
	}
	if _, st := r.lbsLoginAs(token, "new"); st != http.StatusConflict {
		t.Fatal("new account past the cap", st)
	}
	for n := 1; n <= MaxIdentityCarriedAccounts; n++ {
		a, _ := DefaultDB.RegisterAccount("198.51.100.8")
		if list, st := r.carry(token, r.auth.IssueCarryCode(a.LoginKey)); st != 200 || list.Added != MaxIdentityNewAccounts+n {
			t.Fatalf("carry %d: %d %+v", n, st, list)
		}
	}
	extra, _ := DefaultDB.RegisterAccount("198.51.100.8")
	if _, st := r.carry(token, r.auth.IssueCarryCode(extra.LoginKey)); st != http.StatusConflict {
		t.Fatal("carried past the cap", st)
	}
	total := MaxIdentityNewAccounts + MaxIdentityCarriedAccounts
	list, st := r.accounts("/auth/accounts", token)
	if st != 200 || len(list.Accounts) != total || list.Default != 1 || list.MaxNew != MaxIdentityNewAccounts ||
		list.MaxCarried != MaxIdentityCarriedAccounts {
		t.Fatalf("list %d %+v", st, list)
	}
	for i, a := range list.Accounts {
		if a.Carried != (i >= MaxIdentityNewAccounts) {
			t.Fatalf("account %d carried %v", i+1, a.Carried)
		}
	}
	keys := map[string]int{}
	for n := 1; n <= total; n++ {
		got, st := r.lbsLoginAs(token, strconv.Itoa(n))
		a := sessionAccount(got)
		if st != 200 || a == "" || got.Account != n {
			t.Fatalf("login account %d: %d %+v", n, st, got)
		}
		keys[a] = n
	}
	if len(keys) != total {
		t.Fatal("accounts not distinct", keys)
	}
	first, _ := r.lbsLogin(token)
	if a := sessionAccount(first); a == "" || keys[a] != 1 {
		t.Fatal("no account given: not the default one")
	}
	for _, bad := range []string{"0", strconv.Itoa(total + 1), "x", "-1"} {
		if _, st := r.lbsLoginAs(token, bad); st != http.StatusForbidden {
			t.Fatal("account", bad, st)
		}
	}
	if _, st := r.accounts("/auth/accounts", "nope"); st != http.StatusForbidden {
		t.Fatal("unknown token listed", st)
	}

	other := r.tokenFor("6666")
	if otherList, _ := r.accounts("/auth/accounts", other); len(otherList.Accounts) != 0 {
		t.Fatal("identities share accounts", otherList)
	}
	r.lbsLoginAs(other, "new")
	key, _ := DefaultDB.IdentityLoginKey("discord", "6666")
	u, _ := DefaultDB.RegisterUser(key)
	if err := DefaultDB.ReplaceMaster(&Master{Bans: []*MasterBan{{Key: u.UserID, Until: time.Now().Add(time.Hour)}}}); err != nil {
		t.Fatal(err)
	}
	carried, _ := DefaultDB.RegisterAccount("198.51.100.9")
	if _, st := r.carry(other, r.auth.IssueCarryCode(carried.LoginKey)); st != http.StatusForbidden {
		t.Fatal("banned identity carried an account", st)
	}
}

func TestCarryOverMissesLock(t *testing.T) {
	r := newAuthRig(t)
	a, _ := DefaultDB.RegisterAccount("198.51.100.8")
	token := r.tokenFor("5555")
	for i := 0; i < carryMaxMisses; i++ {
		r.carry(token, "WRONGCOD")
	}
	if _, st := r.carry(token, r.auth.IssueCarryCode(a.LoginKey)); st != http.StatusTooManyRequests {
		t.Fatal("good code accepted after the miss limit", st)
	}
}

func TestStartRejectsBadRequests(t *testing.T) {
	r := newAuthRig(t)
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
