package login

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/template"

	"zdxsv/pkg/assets"
	"zdxsv/pkg/config"
	"zdxsv/pkg/db"

	"github.com/golang/glog"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

var (
	tplTop     *template.Template
	tplLogin   *template.Template
	tplMessage *template.Template
)

func Prepare() {
	var err error
	var bin []byte

	bin, err = assets.Asset("assets/top.tpl")
	if err != nil {
		glog.Fatalln(err)
	}

	tplTop, err = template.New("top").Parse(string(bin))
	if err != nil {
		glog.Fatalln(err)
	}

	bin, err = assets.Asset("assets/login.tpl")
	if err != nil {
		glog.Fatalln(err)
	}

	tplLogin, err = template.New("login").Parse(string(bin))
	if err != nil {
		glog.Fatalln(err)
	}

	bin, err = assets.Asset("assets/message.tpl")
	if err != nil {
		glog.Fatalln(err)
	}

	tplMessage, err = template.New("message").Parse(string(bin))
	if err != nil {
		glog.Fatalln(err)
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
	glog.Infoln(r.Form)
	loginKey := r.FormValue("login_key")

	if loginKey == "" {
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, messageLoginFail)
		return
	}

	a, err := loginAccount(r.RemoteAddr, loginKey)
	if err != nil {
		glog.Errorln(err)
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, messageLoginFail)
		return
	}

	p.ServerVersion = "1.0"
	p.LoginKey = a.LoginKey
	p.SessionID = a.SessionID
	p.ServerAddr = config.Conf.Lobby.PublicAddr

	w.Header().Set("Content-Type", "text/html; charset=cp932")
	w.WriteHeader(200)
	sw := japanese.ShiftJIS.NewEncoder().Writer(w)
	tplLogin.Execute(sw, p)
}

func loginAccount(remoteAddr, loginKey string) (*db.Account, error) {
	a, err := db.DefaultDB.GetAccountByLoginKey(loginKey)
	if err == sql.ErrNoRows && len(loginKey) == 10 {
		// Since this login key seems to have been registered on another server,
		// new registration is performed.
		a, err = db.DefaultDB.RegisterAccountWithLoginKey(remoteAddr, loginKey)
	}
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

type apiLoginResponse struct {
	LoginKey  string `json:"login_key"`
	SessionID string `json:"session_id"`
	LobbyAddr string `json:"lobby_addr"`
	LbsInfo   string `json:"lbs_info"`
}

// HandleAPILogin answers the login result in JSON, for clients that skip the
// in-game browser.
func HandleAPILogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	loginKey := r.FormValue("login_key")
	if loginKey == "" {
		http.Error(w, "login_key required", http.StatusBadRequest)
		return
	}
	a, err := loginAccount(r.RemoteAddr, loginKey)
	if err != nil {
		glog.Errorln(err)
		http.Error(w, "login failed", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiLoginResponse{
		LoginKey:  a.LoginKey,
		SessionID: a.SessionID,
		LobbyAddr: config.Conf.Lobby.PublicAddr,
		LbsInfo:   LbsInfo,
	})
}

func HandleRegisterPage(w http.ResponseWriter, r *http.Request) {
	a, err := db.DefaultDB.RegisterAccount(r.RemoteAddr)
	if err != nil {
		glog.Errorln(err)
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
	tplMessage.Execute(sw, p)
}

// HandleTestPage handles a test page for editing HTML.
// Not used in production environments.
func HandleTestPage(w http.ResponseWriter, r *http.Request) {
	glog.Infoln("HandleTestPage")
	file, err := os.Open("work/src/testpage.html")
	if err != nil {
		glog.Errorln(err)
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, "Page not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=cp932")
	w.WriteHeader(200)
	io.Copy(w, transform.NewReader(file, japanese.EUCJP.NewEncoder()))
}
