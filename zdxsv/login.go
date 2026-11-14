package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"sync"
	"text/template"
	"time"

	"go.uber.org/zap"
	"golang.org/x/text/encoding/japanese"
)

var (
	since = time.Now()
)

// assets
func handleAssets(w http.ResponseWriter, r *http.Request) {
	if _, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since")); err == nil {
		h := w.Header()
		delete(h, "Content-Type")
		delete(h, "Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return
	}

	bin, err := Asset(r.URL.Path[1:])
	if err != nil {
		logger.Error("login server failure", zap.Error(err))
		w.WriteHeader(404)
		return
	}

	if m := mime.TypeByExtension(path.Ext(r.URL.Path)); m != "" {
		w.Header().Set("Content-Type", m)
	}

	w.Header().Set("Cache-Control", "max-age=60, public")
	w.Header().Set("Expires", time.Now().Add(60*time.Second).Format(http.TimeFormat))
	w.Header().Set("Last-Modified", since.UTC().Format(http.TimeFormat))

	w.WriteHeader(200)
	w.Write(bin)
}

// health
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf8")
	w.WriteHeader(200)
	fmt.Fprintf(w, "works\n")
}

type wrapper struct {
	http.ResponseWriter
	status int
}

func (r *wrapper) Write(p []byte) (int, error) {
	return r.ResponseWriter.Write(p)
}

func (r *wrapper) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func wrapHandler(f http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := &wrapper{ResponseWriter: w}
		f.ServeHTTP(m, r)

		if r.Method == "POST" {
			r.ParseForm()
		}
		logger.Info("login http", zap.Any("status", m.status), zap.String("addr", r.RemoteAddr), zap.String("method", r.Method), zap.String("path", r.URL.Path), zap.String("query", r.URL.RawQuery), zap.Any("form", r.Form), zap.String("ua", r.UserAgent()))

	}
}

func newLoginHandler() http.Handler {
	PrepareLoginPages()
	router := http.NewServeMux()
	router.HandleFunc("/health", handleHealth)
	router.HandleFunc("/assets/", handleAssets)
	router.HandleFunc("/CRS-top.jsp", HandleTopPage)
	router.HandleFunc("/login", HandleLoginPage)
	router.HandleFunc("/register", HandleRegisterPage)
	return wrapHandler(router)
}

// assetsCheck fails when the embedded assets are missing.
func assetsCheck() ([]byte, error) {
	return Asset("assets/checkfile")
}

var (
	tplTop          *template.Template
	tplLogin        *template.Template
	tplLoginMessage *template.Template
)

func PrepareLoginPages() {
	var err error
	var bin []byte

	bin, err = Asset("assets/top.tpl")
	if err != nil {
		logger.Fatal("login template", zap.Error(err))
	}

	tplTop, err = template.New("top").Parse(string(bin))
	if err != nil {
		logger.Fatal("login template", zap.Error(err))
	}

	bin, err = Asset("assets/login.tpl")
	if err != nil {
		logger.Fatal("login template", zap.Error(err))
	}

	tplLogin, err = template.New("login").Parse(string(bin))
	if err != nil {
		logger.Fatal("login template", zap.Error(err))
	}

	bin, err = Asset("assets/message.tpl")
	if err != nil {
		logger.Fatal("login template", zap.Error(err))
	}

	tplLoginMessage, err = template.New("message").Parse(string(bin))
	if err != nil {
		logger.Fatal("login template", zap.Error(err))
	}
}

var messageRegister = `<br><br><br><br><br><br>
	ユーザ作成が完了しました。 <br>
	メモリカードにIDを保存して戻って下さい。<br>`
var messageLoginFail = `<br><br><br><br><br><br>
	ログインに失敗しました。<br>
	アカウントをお持ちでない場合はTOPから新規登録してください。<br>`
var messageMainte = `<br><br><br><br><br><br>
	現在メンテナンス中です。<br>
	しばらく時間を置いてから再度ログインしてください。<br>`

type commonParam struct {
	ServerVersion string
	LoginKey      string
	SessionID     string
	ServerAddr    string
	Message       string
	GameCode      string
	GameTitle     string
}

func HandleTopPage(w http.ResponseWriter, r *http.Request) {
	p := commonParam{}
	p.ServerVersion = "1.0"
	p.GameTitle = gameTitle(gameCodeOf(r))
	w.Header().Set("Content-Type", "text/html; charset=cp932")
	w.WriteHeader(200)

	sw := japanese.ShiftJIS.NewEncoder().Writer(w)
	tplTop.Execute(sw, p)
}

func HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	p := commonParam{}
	r.ParseForm()
	logger.Info("login form", zap.Any("form", r.Form))
	loginKey := r.FormValue("login_key")

	if loginKey == "" {
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, messageLoginFail)
		return
	}

	a, err := loginAccount(loginKey)
	if err != nil {
		logger.Error("login page failed", zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, messageLoginFail)
		return
	}

	p.ServerVersion = "1.0"
	p.GameCode = gameCodeOf(r)
	p.LoginKey = a.LoginKey
	p.SessionID, _ = Sessions.Start(a.LoginKey, false)
	Sessions.SetGame(p.SessionID, p.GameCode)
	p.ServerAddr = Conf.Lobby.PublicAddr

	w.Header().Set("Content-Type", "text/html; charset=cp932")
	w.WriteHeader(200)
	sw := japanese.ShiftJIS.NewEncoder().Writer(w)
	tplLogin.Execute(sw, p)
}

func loginAccount(loginKey string) (*DBAccount, error) {
	a, err := DefaultDB.GetAccountByLoginKey(loginKey)
	if err != nil {
		return nil, err
	}
	if err := DefaultDB.LoginAccount(a); err != nil {
		return nil, err
	}
	return a, nil
}

// LbsInfo must match the CRS-LBS-INFO-GET entry in assets/login.tpl.
const LbsInfo = "0ad601082008,ZDX BETA SERVER"

type APILoginResponse struct {
	LoginKey  string `json:"login_key"`
	SessionID string `json:"session_id"`
	LobbyAddr string `json:"lobby_addr"`
	LbsInfo   string `json:"lbs_info"`
	// Account is the number (from 1) of the account logged in to among the identity's.
	Account int `json:"account,omitempty"`
}

// HandleAPILogin answers the login result in JSON, for clients that skip the
// in-game browser.
func HandleAPILogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.FormValue("token")
	if token == "" {
		if r.FormValue("login_key") != "" {
			http.Error(w, "login by token only", http.StatusForbidden)
			return
		}
		http.Error(w, "token required", http.StatusBadRequest)
		return
	}
	loginKey, keys, err := DefaultDB.TokenAccounts(token)
	n := r.FormValue("account")
	if err == nil && n == "" && len(keys) == 0 {
		n = "new"
	}
	if err == nil && n == "new" {
		// An account is made at its first login (the identity's first, or one the emulator added), so a
		// mistaken Add makes nothing.
		for _, k := range keys {
			if ban, e := DefaultDB.FindBan(k); e != nil || ban != nil {
				http.Error(w, "banned", http.StatusForbidden)
				return
			}
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		a, e := DefaultDB.AddTokenAccount(token, ip)
		if errors.Is(e, ErrIdentityFull) {
			http.Error(w, "too many accounts", http.StatusConflict)
			return
		}
		if e != nil {
			logger.Error("token login: new account failed", zap.Error(e))
			http.Error(w, "login failed", http.StatusForbidden)
			return
		}
		logger.Info("token login: new account", zap.String("login_key", a.LoginKey))
		loginKey, keys = a.LoginKey, append(keys, a.LoginKey)
	} else if err == nil && n != "" {
		if i, e := strconv.Atoi(n); e == nil && 1 <= i && i <= len(keys) {
			loginKey = keys[i-1]
		} else {
			loginKey = ""
		}
	}
	if err != nil || (loginKey == "" && len(keys) > 0) {
		logger.Error("token login refused", zap.Error(err))
		http.Error(w, "login failed", http.StatusForbidden)
		return
	}
	if loginKey == "" {
		http.Error(w, "no account", http.StatusNotFound)
		return
	}
	a, err := loginAccount(loginKey)
	if err != nil {
		logger.Error("login page failed", zap.Error(err))
		http.Error(w, "login failed", http.StatusForbidden)
		return
	}
	// The emulator gets a key of its session, never the account's own.
	sessionID, gameKey := Sessions.Start(a.LoginKey, true)
	Sessions.SetGame(sessionID, apiGameCode(r.FormValue("game")))
	w.Header().Set("Content-Type", "application/json")
	res := APILoginResponse{
		LoginKey:  gameKey,
		SessionID: sessionID,
		LobbyAddr: Conf.Lobby.PublicAddr,
		LbsInfo:   LbsInfo,
	}
	for i, k := range keys {
		if k == loginKey {
			res.Account = i + 1
		}
	}
	json.NewEncoder(w).Encode(res)
}

func HandleRegisterPage(w http.ResponseWriter, r *http.Request) {
	a, err := DefaultDB.RegisterAccount(r.RemoteAddr)
	if err != nil {
		logger.Error("login page failed", zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, "登録に失敗しました")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=cp932")
	w.WriteHeader(200)
	fmt.Fprintf(w, "<!--COMP-SIGNUP--><!--INPUT-IDS   %s-->\n", a.LoginKey)
	writeMessagePage(w, r, messageRegister)
}

func writeMessagePage(w http.ResponseWriter, r *http.Request, message string) {
	p := commonParam{}
	p.ServerVersion = "1.0"
	p.GameTitle = gameTitle(gameCodeOf(r))
	p.Message = message
	sw := japanese.ShiftJIS.NewEncoder().Writer(w)
	tplLoginMessage.Execute(sw, p)
}

const (
	// SessionTTL is how long a session lasts after its last use.
	SessionTTL = 24 * time.Hour
	// MaxSessionsPerAccount bounds the sessions of an account; the least recently used goes first.
	MaxSessionsPerAccount = 16
)

// Sessions is the store the login pages and the lobby share.
var Sessions = NewSessionStore()

type loginSession struct {
	loginKey string
	tempKey  string
	game     string
	used     time.Time
}

// SessionStore keeps the login sessions in memory. The login pages start one at each
// login of an account, before a user is chosen; the lobby checks it at every connection
// of that client, the one after a battle included. An account holds a session per client,
// so its users can play at once.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*loginSession
	now      func() time.Time
}

func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: map[string]*loginSession{}, now: time.Now}
}

// Start begins a session of the account and returns its id. With temp it also returns a
// key of the session's own that the client logs in with in place of the account's key.
func (s *SessionStore) Start(loginKey string, temp bool) (id, tempKey string) {
	if temp {
		tempKey = GenLoginKey()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var own []string
	for sid, ss := range s.sessions {
		if now.Sub(ss.used) >= SessionTTL {
			delete(s.sessions, sid)
		} else if ss.loginKey == loginKey {
			own = append(own, sid)
		}
	}
	for ; len(own) >= MaxSessionsPerAccount; own = own[:len(own)-1] {
		oldest := 0
		for i, sid := range own {
			if s.sessions[sid].used.Before(s.sessions[own[oldest]].used) {
				oldest = i
			}
		}
		delete(s.sessions, own[oldest])
		own[oldest] = own[len(own)-1]
	}
	for id == "" || s.sessions[id] != nil {
		id = GenSessionID()
	}
	s.sessions[id] = &loginSession{loginKey: loginKey, tempKey: tempKey, used: now}
	return id, tempKey
}

// Use returns the account of session id when key is the account's login key or the
// session's temporary key, and keeps the session for another SessionTTL.
func (s *SessionStore) Use(id, key string) (loginKey string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[id]
	now := s.now()
	if ss == nil || now.Sub(ss.used) >= SessionTTL || key == "" || (key != ss.loginKey && key != ss.tempKey) {
		return "", false
	}
	ss.used = now
	return ss.loginKey, true
}

// SetGame records the game code of the client of session id.
func (s *SessionStore) SetGame(id, game string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.sessions[id]; ss != nil {
		ss.game = game
	}
}

// Game returns the game code of the client of session id; SLPS-25419 when unknown.
func (s *SessionStore) Game(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.sessions[id]; ss != nil && ss.game != "" {
		return ss.game
	}
	return gameCodeZGVSZ
}

// Touch keeps session id for another SessionTTL.
func (s *SessionStore) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.sessions[id]; ss != nil && s.now().Sub(ss.used) < SessionTTL {
		ss.used = s.now()
	}
}

//go:embed assets
var assetFiles embed.FS

// Asset returns the login pages' file at name ("assets/login.png"), served under /assets/.
func Asset(name string) ([]byte, error) {
	return fs.ReadFile(assetFiles, name)
}
