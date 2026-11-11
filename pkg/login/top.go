package login

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"text/template"

	"zdxsv/pkg/config"
	"zdxsv/pkg/db"

	"go.uber.org/zap"
	"golang.org/x/text/encoding/japanese"
	"zdxsv/pkg/zlog"
)

var (
	tplTop     *template.Template
	tplLogin   *template.Template
	tplLoginMessage *template.Template
)

func PrepareLoginPages() {
	var err error
	var bin []byte

	bin, err = Asset("assets/top.tpl")
	if err != nil {
		zlog.Fatal("login template", zap.Error(err))
	}

	tplTop, err = template.New("top").Parse(string(bin))
	if err != nil {
		zlog.Fatal("login template", zap.Error(err))
	}

	bin, err = Asset("assets/login.tpl")
	if err != nil {
		zlog.Fatal("login template", zap.Error(err))
	}

	tplLogin, err = template.New("login").Parse(string(bin))
	if err != nil {
		zlog.Fatal("login template", zap.Error(err))
	}

	bin, err = Asset("assets/message.tpl")
	if err != nil {
		zlog.Fatal("login template", zap.Error(err))
	}

	tplLoginMessage, err = template.New("message").Parse(string(bin))
	if err != nil {
		zlog.Fatal("login template", zap.Error(err))
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
}

func HandleTopPage(w http.ResponseWriter, r *http.Request) {
	p := commonParam{}
	p.ServerVersion = "1.0"
	w.Header().Set("Content-Type", "text/html; charset=cp932")
	w.WriteHeader(200)

	sw := japanese.ShiftJIS.NewEncoder().Writer(w)
	tplTop.Execute(sw, p)
}

func HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	p := commonParam{}
	r.ParseForm()
	zlog.Info("login form", zap.Any("form", r.Form))
	loginKey := r.FormValue("login_key")

	if loginKey == "" {
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, messageLoginFail)
		return
	}

	a, err := loginAccount(loginKey)
	if err != nil {
		zlog.Error("login page failed", zap.Error(err))
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, messageLoginFail)
		return
	}

	p.ServerVersion = "1.0"
	p.LoginKey = a.LoginKey
	p.SessionID, _ = Sessions.Start(a.LoginKey, false)
	p.ServerAddr = config.Conf.Lobby.PublicAddr

	w.Header().Set("Content-Type", "text/html; charset=cp932")
	w.WriteHeader(200)
	sw := japanese.ShiftJIS.NewEncoder().Writer(w)
	tplLogin.Execute(sw, p)
}

func loginAccount(loginKey string) (*db.DBAccount, error) {
	a, err := db.DefaultDB.GetAccountByLoginKey(loginKey)
	if err != nil {
		return nil, err
	}
	if err := db.DefaultDB.LoginAccount(a); err != nil {
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
	loginKey, keys, err := db.DefaultDB.TokenAccounts(token)
	n := r.FormValue("account")
	if err == nil && n == "" && len(keys) == 0 {
		n = "new"
	}
	if err == nil && n == "new" {
		// An account is made at its first login (the identity's first, or one the emulator added), so a
		// mistaken Add makes nothing.
		for _, k := range keys {
			if ban, e := db.DefaultDB.FindBan(k); e != nil || ban != nil {
				http.Error(w, "banned", http.StatusForbidden)
				return
			}
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		a, e := db.DefaultDB.AddTokenAccount(token, ip)
		if errors.Is(e, db.ErrIdentityFull) {
			http.Error(w, "too many accounts", http.StatusConflict)
			return
		}
		if e != nil {
			zlog.Error("token login: new account failed", zap.Error(e))
			http.Error(w, "login failed", http.StatusForbidden)
			return
		}
		zlog.Info("token login: new account", zap.String("login_key", a.LoginKey))
		loginKey, keys = a.LoginKey, append(keys, a.LoginKey)
	} else if err == nil && n != "" {
		if i, e := strconv.Atoi(n); e == nil && 1 <= i && i <= len(keys) {
			loginKey = keys[i-1]
		} else {
			loginKey = ""
		}
	}
	if err != nil || (loginKey == "" && len(keys) > 0) {
		zlog.Error("token login refused", zap.Error(err))
		http.Error(w, "login failed", http.StatusForbidden)
		return
	}
	if loginKey == "" {
		http.Error(w, "no account", http.StatusNotFound)
		return
	}
	a, err := loginAccount(loginKey)
	if err != nil {
		zlog.Error("login page failed", zap.Error(err))
		http.Error(w, "login failed", http.StatusForbidden)
		return
	}
	// The emulator gets a key of its session, never the account's own.
	sessionID, gameKey := Sessions.Start(a.LoginKey, true)
	w.Header().Set("Content-Type", "application/json")
	res := APILoginResponse{
		LoginKey:  gameKey,
		SessionID: sessionID,
		LobbyAddr: config.Conf.Lobby.PublicAddr,
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
	a, err := db.DefaultDB.RegisterAccount(r.RemoteAddr)
	if err != nil {
		zlog.Error("login page failed", zap.Error(err))
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
	p.Message = message
	sw := japanese.ShiftJIS.NewEncoder().Writer(w)
	tplLoginMessage.Execute(sw, p)
}
