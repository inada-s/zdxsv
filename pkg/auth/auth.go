// Package auth signs emulators in through an OAuth provider (Discord).
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
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"zdxsv/pkg/db"

	"zdxsv/pkg/zlog"
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

func Discord(clientID, clientSecret string) Provider {
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
	RegisterAccount(ip string) (*db.Account, error)
	AccountCarried(loginKey string) (bool, error)
	GetUserList(loginKey string) ([]*db.User, error)
	FindBan(loginKey string) (*db.MasterBan, error)
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

type Server struct {
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

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

const carryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// IssueCarryCode returns a single use code that links loginKey to a signed-in identity.
func (s *Server) IssueCarryCode(loginKey string) string {
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

func (s *Server) gc() {
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

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/auth/start":
		s.serveStart(w, r)
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

func (s *Server) redirectURI() string {
	return strings.TrimSuffix(s.PublicURL, "/") + "/auth/" + s.Provider.Name + "/callback"
}

func (s *Server) serveStart(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	port, err := strconv.Atoi(q.Get("redirect_port"))
	state := q.Get("state")
	challenge := q.Get("code_challenge")
	if err != nil || port < 1024 || port > 65535 || state == "" || len(state) > 128 || !validChallenge(challenge) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id := randomString(24)
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

func (s *Server) fail(w http.ResponseWriter, r *http.Request, msg func(*page) string) {
	p := pageText(pickLang(r))
	p.Message = msg(&p)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	tplMessage.Execute(w, p)
}

func (s *Server) serveCallback(w http.ResponseWriter, r *http.Request) {
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
		zlog.Errorln("auth provider:", err)
		s.fail(w, r, func(p *page) string { return p.ProviderError })
		return
	}
	code := randomString(24)
	s.mu.Lock()
	if s.codes == nil {
		s.codes = map[string]*issued{}
	}
	s.codes[code] = &issued{userID: userID, challenge: f.challenge, expires: s.now().Add(codeTTL)}
	s.mu.Unlock()
	back := url.Values{"code": {code}, "state": {f.state}}
	http.Redirect(w, r, fmt.Sprintf("http://127.0.0.1:%d/callback?%s", f.port, back.Encode()), http.StatusFound)
}

func (s *Server) providerUser(code string) (string, error) {
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

func (s *Server) serveToken(w http.ResponseWriter, r *http.Request) {
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
		zlog.Errorln("auth token:", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func (s *Server) serveRevoke(w http.ResponseWriter, r *http.Request) {
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
		zlog.Errorln("auth revoke:", err)
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

func (s *Server) serveAccounts(w http.ResponseWriter, r *http.Request, op accountsOp) {
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
	}{Accounts: []accountEntry{}, MaxNew: db.MaxIdentityNewAccounts, MaxCarried: db.MaxIdentityCarriedAccounts}
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
func (s *Server) carryAccount(w http.ResponseWriter, r *http.Request, token string) string {
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
	case errors.Is(err, db.ErrCarryFull):
		http.Error(w, "too many carried accounts", http.StatusConflict)
		return ""
	case errors.Is(err, db.ErrAccountLinked):
		http.Error(w, "account linked already", http.StatusUnprocessableEntity)
		return ""
	case err != nil:
		zlog.Errorln("auth carry:", err)
		http.Error(w, "", http.StatusForbidden)
		return ""
	}
	zlog.Infoln("auth: carried account", c.loginKey)
	return c.loginKey
}
