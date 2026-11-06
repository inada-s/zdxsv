package lobby

import (
	"zdxsv/pkg/db"

	"github.com/golang/glog"
)

const carryCommand = "ｄｃ"

// SetCarryCodeIssuer turns the carry-over chat command on: issue returns a code that links a
// Discord sign-in to the account.
func (a *App) SetCarryCodeIssuer(issue func(loginKey string) string) {
	a.carryCode = issue
}

func lobbyChatCarry(p *AppPeer, str string) bool {
	if str != carryCommand || p.app.carryCode == nil || p.LoginKey == "" {
		return false
	}
	ban, err := db.DefaultDB.FindBan(p.LoginKey)
	if err != nil || ban != nil {
		glog.Infoln("carry code refused", p.LoginKey, err)
		NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコードを発行できません")
		return true
	}
	code := p.app.carryCode(p.LoginKey)
	glog.Infoln("carry code issued", p.LoginKey)
	NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコード "+code)
	NoticeChatMessage(p, "SERVER", "＞", "１０分以内に一度だけ使えます")
	NoticeChatMessage(p, "SERVER", "＞", "他人に教えないでください")
	return true
}
