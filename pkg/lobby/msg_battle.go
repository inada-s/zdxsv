package lobby

import (
	"strconv"
	"strings"
	. "zdxsv/pkg/lobby/message"

	"github.com/golang/glog"
)

func NoticeBattleStart(p *AppPeer) {
	n := NewServerNotice(0x6910)
	p.SendMessage(n)
}

var _ = register(0x6911, "GetBattleUserCount", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	count := p.app.OnGetBattleUserCount(p)
	w := a.Writer()
	if count > 0 {
		w.Write8(count) // ユーザ数
	} else {
		a.Status = StatusError
	}
	p.SendMessage(a)
})

var _ = register(0x6912, "GetBattleUserPosition", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	pos := p.app.OnGetBattleUserPosition(p)
	if pos > 0 {
		w.Write8(pos)
	} else {
		a.Status = StatusError
	}
	p.SendMessage(a)
})

var _ = register(0x6913, "GetBattleOpponentUser", func(p *AppPeer, m *Message) {
	pos := m.Reader().Read8()
	user := p.app.OnGetBattleOpponentUser(p, pos)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(pos)
	if user != nil {
		w.Write8(user.Entry)
		w.WriteString(user.UserId)
		w.WriteString(user.Name)
		w.WriteString(user.Team)
		w.WriteString(user.Bin)
		w.Write8(pos) // 不明
	} else {
		glog.Infoln("UserPos not found", pos)
		a.Status = StatusError
	}
	p.SendMessage(a)
})

var _ = register(0x6917, "GetBattleOpponentStatus", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	// TODO:戦績データ? 何か調べる
	p.SendMessage(a)
})

var _ = register(0x6914, "GetBattleRule", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	rule := p.app.OnGetBattleRule(p)
	w.Write(rule.Serialize())
	p.SendMessage(a)
})

var _ = register(0x6915, "GetBattleBattleCode", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	w.WriteString("123") // 書き方がよくわからん
	p.SendMessage(a)
})

var _ = register(0x6916, "GetBattleServerAddress", func(p *AppPeer, m *Message) {
	ip, port := p.app.OnGetBattleServerAddress(p)
	a := NewServerAnswer(m)

	if ip == nil || ip.To4() == nil || port == 0 {
		a.Status = StatusError
	} else {
		bits := strings.Split(ip.String(), ".")
		b0, _ := strconv.Atoi(bits[0])
		b1, _ := strconv.Atoi(bits[1])
		b2, _ := strconv.Atoi(bits[2])
		b3, _ := strconv.Atoi(bits[3])
		w := a.Writer()
		w.Write16(4)
		w.Write8(byte(b0))
		w.Write8(byte(b1))
		w.Write8(byte(b2))
		w.Write8(byte(b3))
		w.Write16(2)
		w.Write16(port)
	}
	p.SendMessage(a)
})

var _ = register(0x6210, "EnterBattleAfterRoom", func(p *AppPeer, m *Message) {
	p.app.OnEnterBattleAfterRoom(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6211, "ExitBattleAfterRoom", func(p *AppPeer, m *Message) {
	p.app.OnExitBattleAfterRoom(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6212, "GetBattleAfterRoomUserCount", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	n := p.app.OnGetBattleAfterRoomUserCount(p)
	w.Write16(n)
	p.SendMessage(a)
})

func NoticeBattleAfterRoomUserCount(p *AppPeer, count uint16) {
	n := NewServerNotice(0x6212)
	w := n.Writer()
	w.Write16(count)
	p.SendMessage(n)
}
