package lobby

import (
	"zdxsv/pkg/db"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
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
		zlog.Info("carry code refused", zap.String("login_key", p.LoginKey), zap.Error(err))
		NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコードを発行できません")
		return true
	}
	if n, err := db.DefaultDB.AccountBattleCount(p.LoginKey); err != nil || n < db.CarryMinBattles {
		zlog.Info("carry code refused: battles", zap.String("login_key", p.LoginKey), zap.Any("battles", n), zap.Error(err))
		NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコードは１００戦以上で発行できます")
		return true
	}
	code := p.app.carryCode(p.LoginKey)
	zlog.Info("carry code issued", zap.String("login_key", p.LoginKey))
	NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコード "+code)
	NoticeChatMessage(p, "SERVER", "＞", "３分以内に一度だけ使えます")
	NoticeChatMessage(p, "SERVER", "＞", "他人に教えないでください")
	return true
}
