// The auth server signs emulators in through an OAuth provider (Discord).
//
//	GET  /auth/start?redirect_port=P&state=S&code_challenge=C[&lang=ja|en|zh-TW]:
//	     a notice page with the provider button. C is the PKCE S256 challenge.
//	GET  /auth/discord/callback: the provider's redirect; ends at http://127.0.0.1:P/callback?code=X&state=S.
//	POST /auth/token (form code, code_verifier): {"token": T}, the emulator's token of the provider
//	     identity, for /lbs/login. A sign-in creates no account: the identity may hold none.
//	POST /auth/revoke (form token): deletes the token.
//	POST /auth/accounts (form token): {"accounts": [{"account": N, "users": [name...], "carried": B}], "default": N,
//	     "max_new": A, "max_carried": C},
//	     the accounts of the token's identity; /lbs/login takes account=N, without it "default", and
//	     account=new makes the identity's account at that login (409 past max_new, 403 banned);
//	     the identity's first login with no account makes one too.
//	     "default" is 0 while the identity holds no account.
//	POST /auth/accounts/carry (form token, code): links the account of a carry-over code (lobby chat
//	     ｄｃ on a PS2) to the identity, answers the same with "added": N; 400 a wrong or expired
//	     code, 429 after too many wrong codes, 409 the identity carried max_carried, 422 the account is
//	     linked already, 403 banned.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	flowTTL        = 10 * time.Minute
	codeTTL        = 2 * time.Minute
	carryTTL       = 3 * time.Minute
	carryMaxMisses = 5
	maxPending     = 10000
)

// Provider is an OAuth2 authorization code provider whose user endpoint answers {"id": ...}.
type Provider struct {
	Name         string
	AuthURL      string
	TokenURL     string
	UserURL      string
	Scope        string
	ClientID     string
	ClientSecret string
	dev          bool
}

func DiscordProvider(clientID, clientSecret string) Provider {
	return Provider{
		Name:         "discord",
		AuthURL:      "https://discord.com/oauth2/authorize",
		TokenURL:     "https://discord.com/api/oauth2/token",
		UserURL:      "https://discord.com/api/users/@me",
		Scope:        "identify",
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
}

// Store is the part of db.DB the sign-in uses.
type Store interface {
	IssueAuthToken(provider, providerUserID string) (string, error)
	RevokeAuthToken(token string) error
	TokenAccounts(token string) (string, []string, error)
	CarryTokenAccount(token, loginKey string) error
	RegisterAccount(ip string) (*DBAccount, error)
	AccountCarried(loginKey string) (bool, error)
	GetUserList(loginKey string) ([]*DBUser, error)
	FindBan(loginKey string) (*MasterBan, error)
}

type flow struct {
	port      int
	state     string
	challenge string
	expires   time.Time
}

type issued struct {
	userID    string
	challenge string
	expires   time.Time
}

type carryCode struct {
	loginKey string
	expires  time.Time
}

type AuthServer struct {
	Provider Provider
	// PublicURL is where players reach /auth/ (https://host), the provider's redirect base.
	PublicURL string
	Store     Store
	Client    *http.Client
	Now       func() time.Time

	mu     sync.Mutex
	flows  map[string]*flow
	codes  map[string]*issued
	carry  map[string]*carryCode
	misses map[string]int
}

func (s *AuthServer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *AuthServer) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func authRandomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

const carryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// IssueCarryCode returns a single use code that links loginKey to a signed-in identity.
func (s *AuthServer) IssueCarryCode(loginKey string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = carryAlphabet[int(b[i])%len(carryAlphabet)]
	}
	code := string(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gc()
	if s.carry == nil {
		s.carry = map[string]*carryCode{}
	}
	for k, c := range s.carry {
		if c.loginKey == loginKey {
			delete(s.carry, k)
		}
	}
	s.carry[code] = &carryCode{loginKey: loginKey, expires: s.now().Add(carryTTL)}
	return code
}

func (s *AuthServer) gc() {
	now := s.now()
	for k, f := range s.flows {
		if now.After(f.expires) {
			delete(s.flows, k)
		}
	}
	for k, c := range s.codes {
		if now.After(c.expires) {
			delete(s.codes, k)
		}
	}
	for k, c := range s.carry {
		if now.After(c.expires) {
			delete(s.carry, k)
		}
	}
}

func (s *AuthServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/auth/start":
		s.serveStart(w, r)
	case "/auth/icon.png":
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(authIcon)
	case "/auth/" + s.Provider.Name + "/callback":
		s.serveCallback(w, r)
	case "/auth/token":
		s.serveToken(w, r)
	case "/auth/revoke":
		s.serveRevoke(w, r)
	case "/auth/accounts":
		s.serveAccounts(w, r, accountsList)
	case "/auth/accounts/carry":
		s.serveAccounts(w, r, accountsCarry)
	case "/auth/dev/authorize":
		if !s.Provider.dev {
			http.NotFound(w, r)
			return
		}
		s.serveDevAuthorize(w, r)
	case "/auth/dev/carry":
		if !s.Provider.dev {
			http.NotFound(w, r)
			return
		}
		s.serveDevCarry(w, r)
	default:
		http.NotFound(w, r)
	}
}

func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}

func (s *AuthServer) redirectURI() string {
	return strings.TrimSuffix(s.PublicURL, "/") + "/auth/" + s.Provider.Name + "/callback"
}

func (s *AuthServer) serveStart(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	port, err := strconv.Atoi(q.Get("redirect_port"))
	state := q.Get("state")
	challenge := q.Get("code_challenge")
	if err != nil || port < 1024 || port > 65535 || state == "" || len(state) > 128 || !validChallenge(challenge) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id := authRandomString(24)
	s.mu.Lock()
	s.gc()
	if s.flows == nil {
		s.flows = map[string]*flow{}
	}
	if len(s.flows) >= maxPending {
		s.mu.Unlock()
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	s.flows[id] = &flow{port: port, state: state, challenge: challenge, expires: s.now().Add(flowTTL)}
	s.mu.Unlock()

	v := url.Values{
		"response_type": {"code"},
		"client_id":     {s.Provider.ClientID},
		"scope":         {s.Provider.Scope},
		"redirect_uri":  {s.redirectURI()},
		"state":         {id},
		"prompt":        {"none"},
	}
	p := pageText(pickLang(r))
	p.ProviderURL = s.Provider.AuthURL + "?" + v.Encode()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	tplStart.Execute(w, p)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *AuthServer) fail(w http.ResponseWriter, r *http.Request, msg func(*page) string) {
	p := pageText(pickLang(r))
	p.Message = msg(&p)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	tplAuthMessage.Execute(w, p)
}

func (s *AuthServer) serveCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	f := s.flows[q.Get("state")]
	delete(s.flows, q.Get("state"))
	s.mu.Unlock()
	if f == nil || s.now().After(f.expires) {
		s.fail(w, r, func(p *page) string { return p.Expired })
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		s.fail(w, r, func(p *page) string { return p.Denied })
		return
	}
	userID, err := s.providerUser(q.Get("code"))
	if err != nil {
		logger.Error("auth provider failure", zap.Error(err))
		s.fail(w, r, func(p *page) string { return p.ProviderError })
		return
	}
	code := authRandomString(24)
	s.mu.Lock()
	if s.codes == nil {
		s.codes = map[string]*issued{}
	}
	s.codes[code] = &issued{userID: userID, challenge: f.challenge, expires: s.now().Add(codeTTL)}
	s.mu.Unlock()
	back := url.Values{"code": {code}, "state": {f.state}}
	http.Redirect(w, r, fmt.Sprintf("http://127.0.0.1:%d/callback?%s", f.port, back.Encode()), http.StatusFound)
}

func (s *AuthServer) providerUser(code string) (string, error) {
	if s.Provider.dev {
		if !validDevUserID(code) {
			return "", fmt.Errorf("dev: bad user id %q", code)
		}
		return code, nil
	}
	resp, err := s.client().PostForm(s.Provider.TokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.redirectURI()},
		"client_id":     {s.Provider.ClientID},
		"client_secret": {s.Provider.ClientSecret},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("token: %d %s", resp.StatusCode, b)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("token: no access_token (%v)", err)
	}
	req, _ := http.NewRequest(http.MethodGet, s.Provider.UserURL, nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uresp, err := s.client().Do(req)
	if err != nil {
		return "", err
	}
	defer uresp.Body.Close()
	if uresp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("user: %d", uresp.StatusCode)
	}
	var u struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(uresp.Body, 1<<16)).Decode(&u); err != nil || u.ID == "" || len(u.ID) > 64 {
		return "", fmt.Errorf("user: no id (%v)", err)
	}
	return u.ID, nil
}

func (s *AuthServer) serveToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code := r.FormValue("code")
	verifier := r.FormValue("code_verifier")
	s.mu.Lock()
	c := s.codes[code]
	delete(s.codes, code)
	s.mu.Unlock()
	sum := sha256.Sum256([]byte(verifier))
	if c == nil || s.now().After(c.expires) || len(verifier) < 43 || len(verifier) > 128 ||
		subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(c.challenge)) != 1 {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
		return
	}
	token, err := s.Store.IssueAuthToken(s.Provider.Name, c.userID)
	if err != nil {
		logger.Error("auth token failure", zap.Error(err))
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func (s *AuthServer) serveRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.FormValue("token")
	if token == "" {
		http.Error(w, "token required", http.StatusBadRequest)
		return
	}
	if err := s.Store.RevokeAuthToken(token); err != nil {
		logger.Error("auth revoke failure", zap.Error(err))
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type accountEntry struct {
	Account int      `json:"account"`
	Users   []string `json:"users"`
	Carried bool     `json:"carried"`
}

type accountsOp int

const (
	accountsList accountsOp = iota
	accountsCarry
)

func (s *AuthServer) serveAccounts(w http.ResponseWriter, r *http.Request, op accountsOp) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.FormValue("token")
	key, keys, err := s.Store.TokenAccounts(token)
	if err != nil {
		http.Error(w, "unknown token", http.StatusForbidden)
		return
	}
	added := ""
	if op != accountsList {
		for _, k := range keys {
			if ban, err := s.Store.FindBan(k); err != nil || ban != nil {
				http.Error(w, "banned", http.StatusForbidden)
				return
			}
		}
		added = s.carryAccount(w, r, token)
		if added == "" {
			return
		}
		if key, keys, err = s.Store.TokenAccounts(token); err != nil {
			http.Error(w, "", http.StatusInternalServerError)
			return
		}
	}
	res := struct {
		Accounts   []accountEntry `json:"accounts"`
		Default    int            `json:"default"`
		MaxNew     int            `json:"max_new"`
		MaxCarried int            `json:"max_carried"`
		Added      int            `json:"added,omitempty"`
	}{Accounts: []accountEntry{}, MaxNew: MaxIdentityNewAccounts, MaxCarried: MaxIdentityCarriedAccounts}
	for i, k := range keys {
		e := accountEntry{Account: i + 1, Users: []string{}}
		e.Carried, _ = s.Store.AccountCarried(k)
		users, _ := s.Store.GetUserList(k)
		for _, u := range users {
			e.Users = append(e.Users, u.Name)
		}
		res.Accounts = append(res.Accounts, e)
		if k == key {
			res.Default = i + 1
		}
		if k == added {
			res.Added = i + 1
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(res)
}

// carryAccount returns the carried account's login key, or "" after answering the error.
func (s *AuthServer) carryAccount(w http.ResponseWriter, r *http.Request, token string) string {
	ip := clientIP(r)
	code := strings.ToUpper(strings.TrimSpace(r.FormValue("code")))
	s.mu.Lock()
	if s.misses[ip] >= carryMaxMisses {
		s.mu.Unlock()
		http.Error(w, "too many wrong codes", http.StatusTooManyRequests)
		return ""
	}
	c := s.carry[code]
	delete(s.carry, code)
	if c == nil || s.now().After(c.expires) {
		if s.misses == nil {
			s.misses = map[string]int{}
		}
		s.misses[ip]++
		s.mu.Unlock()
		http.Error(w, "wrong or expired code", http.StatusBadRequest)
		return ""
	}
	s.mu.Unlock()
	if ban, err := s.Store.FindBan(c.loginKey); err != nil || ban != nil {
		http.Error(w, "banned", http.StatusForbidden)
		return ""
	}
	switch err := s.Store.CarryTokenAccount(token, c.loginKey); {
	case errors.Is(err, ErrCarryFull):
		http.Error(w, "too many carried accounts", http.StatusConflict)
		return ""
	case errors.Is(err, ErrAccountLinked):
		http.Error(w, "account linked already", http.StatusUnprocessableEntity)
		return ""
	case err != nil:
		logger.Error("auth carry failure", zap.Error(err))
		http.Error(w, "", http.StatusForbidden)
		return ""
	}
	logger.Info("auth: carried account", zap.String("login_key", c.loginKey))
	return c.loginKey
}

// DevProvider signs in without an outside provider: /auth/dev/authorize asks the browser for a user
// ID and passes it back as the code. For local development only; its identities are stored under the
// provider name "dev", so they never stand for Discord users.
func DevProvider() Provider {
	return Provider{Name: "dev", AuthURL: "/auth/dev/authorize", dev: true}
}

func validDevUserID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

var tplDevAuthorize = template.Must(template.New("dev").Parse(`<!DOCTYPE html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>zdxsv dev sign-in</title>
<style>body{font-family:sans-serif;max-width:36em;margin:3em auto;padding:0 1em;line-height:1.6}</style>
</head><body><h1>zdxsv dev sign-in</h1>
<p>Local development provider. A user ID stands for one provider user; the first sign-in of an ID creates its account.</p>
<form method="get" action="{{.Callback}}"><input type="hidden" name="state" value="{{.State}}">
<p><label>User ID <input name="code" value="1" pattern="[0-9A-Za-z_\-]{1,32}" required autofocus></label>
<button type="submit">Sign in</button></p></form>
<p><a href="{{.Deny}}">Deny</a></p></body></html>`))

func (s *AuthServer) serveDevAuthorize(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" || len(state) > 128 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	tplDevAuthorize.Execute(w, struct{ Callback, State, Deny string }{
		Callback: s.redirectURI(),
		State:    state,
		Deny:     s.redirectURI() + "?" + url.Values{"error": {"access_denied"}, "state": {state}}.Encode(),
	})
}

// serveDevCarry (POST) stands in for the lobby chat ｄｃ of a PS2: it makes a new account, as a PS2
// registration does, and answers {"code": C, "login_key": K} with a carry-over code for it.
func (s *AuthServer) serveDevCarry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a, err := s.Store.RegisterAccount(clientIP(r))
	if err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"code": s.IssueCarryCode(a.LoginKey), "login_key": a.LoginKey})
}

type page struct {
	Lang          string
	Title         string
	Notice        []string
	Button        string
	ProviderURL   string
	Message       string
	Expired       string
	Denied        string
	ProviderError string
	Privacy       string
}

var pages = map[string]page{
	"ja": {
		Lang:  "ja",
		Title: "ZDXSV にサインイン",
		Notice: []string{
			"サインインが終わったらエミュレーターに戻ってください。",
		},
		Button:        "Discord でサインイン",
		Expired:       "サインインの期限が切れました。エミュレーターからやり直してください。",
		Denied:        "サインインが取り消されました。",
		ProviderError: "サインインに失敗しました。しばらくしてからやり直してください。",
		Privacy:       "プライバシーポリシー",
	},
	"en": {
		Lang:  "en",
		Title: "Sign in to ZDXSV",
		Notice: []string{
			"When the sign-in is done, go back to the emulator.",
		},
		Button:        "Sign in with Discord",
		Expired:       "The sign-in expired. Start again from the emulator.",
		Denied:        "The sign-in was cancelled.",
		ProviderError: "The sign-in failed. Try again later.",
		Privacy:       "Privacy Policy",
	},
	"zh-TW": {
		Lang:  "zh-TW",
		Title: "登入 ZDXSV",
		Notice: []string{
			"登入完成後，請回到模擬器。",
		},
		Button:        "使用 Discord 登入",
		Expired:       "登入已逾時，請從模擬器重新開始。",
		Denied:        "登入已取消。",
		ProviderError: "登入失敗，請稍後再試。",
		Privacy:       "隱私權政策",
	},
}

func pickLang(r *http.Request) string {
	if l := r.URL.Query().Get("lang"); l != "" {
		if _, ok := pages[l]; ok {
			return l
		}
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "ja"):
			return "ja"
		case strings.HasPrefix(tag, "zh"):
			return "zh-TW"
		case strings.HasPrefix(tag, "en"):
			return "en"
		}
	}
	return "ja"
}

func pageText(lang string) page {
	return pages[lang]
}

//go:embed auth_icon.png
var authIcon []byte

const head = `<!DOCTYPE html><html lang="{{.Lang}}"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title>
<link rel="icon" href="/auth/icon.png">
<style>
:root{--bg1:#e9edf7;--bg2:#d5dcef;--card:#fff;--fg:#1d2433;--sub:#5b6478;--line:#e3e7f0}
@media (prefers-color-scheme:dark){:root{--bg1:#10131c;--bg2:#1b2133;--card:#1e2433;--fg:#e8ebf3;--sub:#9aa3b8;--line:#2c3447}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px 16px;
background:linear-gradient(160deg,var(--bg1),var(--bg2));color:var(--fg);line-height:1.7;
font-family:system-ui,-apple-system,"Segoe UI","Hiragino Sans","Noto Sans JP","Microsoft JhengHei",sans-serif}
main{text-align:center;width:100%;max-width:26rem;background:var(--card);border-radius:16px;padding:2.25rem 2rem 2rem;
box-shadow:0 10px 40px rgba(20,30,60,.18);border-top:4px solid #5865f2}
.logo{display:block;width:56px;height:56px;border-radius:12px;margin:0 auto 1rem}
h1{font-size:1.45rem;margin:0 0 1.25rem}
p{margin:0 0 .9rem;color:var(--sub);font-size:.95rem}
.act{margin:1.6rem 0 0;padding-top:1.4rem;border-top:1px solid var(--line)}
a.b{display:flex;align-items:center;justify-content:center;gap:.6em;background:#5865f2;color:#fff;font-weight:600;padding:.85em 1em;
border-radius:10px;text-decoration:none;transition:background .15s,transform .15s}
a.b svg{width:1.4em;height:1.1em;flex:none}
.pp{margin:1rem 0 0;font-size:.8rem}.pp a{color:var(--sub)}
a.b:hover{background:#4752c4}a.b:active{transform:translateY(1px)}
</style>
</head><body><main><img class="logo" src="/auth/icon.png" alt=""><h1>{{.Title}}</h1>`

var tplStart = template.Must(template.New("start").Parse(head +
	`{{range .Notice}}<p>{{.}}</p>{{end}}
<p class="act"><a class="b" href="{{.ProviderURL}}"><svg viewBox="0 0 127.14 96.36" aria-hidden="true"><path fill="currentColor" d="M107.7,8.07A105.15,105.15,0,0,0,81.47,0a72.06,72.06,0,0,0-3.36,6.83A97.68,97.68,0,0,0,49,6.83,72.37,72.37,0,0,0,45.64,0,105.89,105.89,0,0,0,19.39,8.09C2.79,32.65-1.71,56.6.54,80.21h0A105.73,105.73,0,0,0,32.71,96.36,77.7,77.7,0,0,0,39.6,85.25a68.42,68.42,0,0,1-10.85-5.18c.91-.66,1.8-1.34,2.66-2a75.57,75.57,0,0,0,64.32,0c.87.71,1.76,1.39,2.66,2a68.68,68.68,0,0,1-10.87,5.19,77,77,0,0,0,6.89,11.1A105.25,105.25,0,0,0,126.6,80.22h0C129.24,52.84,122.09,29.11,107.7,8.07ZM42.45,65.69C36.18,65.69,31,60,31,53s5-12.74,11.44-12.74S54,46,53.89,53,48.84,65.69,42.45,65.69Zm42.24,0C78.41,65.69,73.25,60,73.25,53s5-12.74,11.44-12.74S96.23,46,96.12,53,91.08,65.69,84.69,65.69Z"/></svg>{{.Button}}</a></p>
<p class="pp"><a href="https://www.zdxsv.net/privacy.html">{{.Privacy}}</a></p></main></body></html>`))

var tplAuthMessage = template.Must(template.New("message").Parse(head + `<p>{{.Message}}</p></main></body></html>`))
