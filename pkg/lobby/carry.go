package lobby

import (
	"zdxsv/pkg/db"

	"github.com/golang/glog"
)

const carryCommand = "ｄｃ"

// SetCarryCodeIssuer turns the carry-over chat command on (chat_command lobbies): issue returns a code that
// links the account to a Discord sign-in in the emulator.
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
	if n, err := db.DefaultDB.AccountBattleCount(p.LoginKey); err != nil || n < db.CarryMinBattles {
		glog.Infoln("carry code refused: battles", p.LoginKey, n, err)
		NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコードは１００戦以上で発行できます")
		return true
	}
	code := p.app.carryCode(p.LoginKey)
	glog.Infoln("carry code issued", p.LoginKey)
	NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコード "+code)
	NoticeChatMessage(p, "SERVER", "＞", "３分以内に一度だけ使えます")
	NoticeChatMessage(p, "SERVER", "＞", "他人に教えないでください")
	return true
}
