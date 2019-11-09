package login

import (
	"database/sql"
	"fmt"
	"net/http"
	"text/template"

	"zdxsv/pkg/assets"
	"zdxsv/pkg/config"
	"zdxsv/pkg/db"

	"github.com/golang/glog"
	"golang.org/x/text/encoding/japanese"
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

	a, err := db.DefaultDB.GetAccountByLoginKey(loginKey)
	if err == sql.ErrNoRows && len(loginKey) == 10 {
		// Since this login key seems to have been registered on another server,
		// new registration is performed.
		a, err = db.DefaultDB.RegisterAccountWithLoginKey(r.RemoteAddr, loginKey)
	}

	if err != nil {
		glog.Errorln(err)
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, messageLoginFail)
		return
	}

	err = db.DefaultDB.LoginAccount(a)

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

func HandleRegisterPage(w http.ResponseWriter, r *http.Request) {
	a, err := db.DefaultDB.RegisterAccount(r.RemoteAddr)
	if err != nil {
		glog.Errorln(err)
		w.Header().Set("Content-Type", "text/html; charset=cp932")
		w.WriteHeader(200)
		writeMessagePage(w, r, "登録に失敗しました")
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
