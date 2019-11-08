package lobby

import (
	"strconv"
	"strings"
	. "zdxsv/pkg/lobby/message"

	"zdxsv/pkg/lobby/model"

	"encoding/json"

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
	pos := m.Reader().Read8()
	a := NewServerAnswer(m)
	w := a.Writer()
	user := p.app.OnGetBattleOpponentUser(p, pos)
	w.Write8(pos)
	// class 14 ~ 0 : [大将][中将][少将][大佐][中佐][少佐][大尉][中尉][少尉][曹長][軍曹][伍長][上等兵][一等兵][二等兵]
	// Tekitou
	c := uint16(user.WinCount / 100)
	if 14 <= c {
		c = 14
	}
	w.Write16(c)
	w.Write32(0) // Unknown
	w.Write32(uint32(user.BattleCount))
	w.Write32(uint32(user.WinCount))
	w.Write32(uint32(user.LoseCount))
	w.Write32(0) // Unknown

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
	battleCode, err := p.app.OnGetBattleCode(p)
	if err == nil {
		w := a.Writer()
		w.WriteString(battleCode)
	} else {
		glog.Infoln("Failed to get battle code")
		a.Status = StatusError
	}
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

func AskBattleResult(p *AppPeer) {
	p.SendMessage(NewServerQuestion(0x6138))
}

func parseBattleResult(m *Message) *model.BattleResult {
	r := m.Reader()
	v01 := r.Read16()
	v02 := r.ReadEncryptedString()
	v03 := r.Read8()
	v04 := r.Read8()
	v05 := r.Read8()
	v06 := r.Read8()
	v07 := r.Read8()
	v08 := r.Read8()
	v09 := r.Read8()
	v10 := r.Read32()
	v11 := r.Read32()
	v12 := r.Read32()
	v13 := r.Read32()
	v14 := r.Read32()
	v15 := r.Read8()
	v16 := r.Read8()
	v17 := r.Read8()
	v18 := r.Read8()
	v19 := r.Read8()
	v20 := r.Read16()
	v21 := r.Read16()
	v22 := r.Read16()
	v23 := r.Read16()
	v24 := r.Read16()
	v25 := r.Read16()
	v26 := r.Read16()
	v27 := r.Read16()
	return &model.BattleResult{
		v01, v02, v03, v04, v05, v06, v07, v08,
		v09, v10, v11, v12, v13, v14, v15, v16,
		v17, v18, v19, v20, v21, v22, v23, v24,
		v25, v26, v27,
	}
}

var _ = register(0x6138, "AnswerBattleResult", func(p *AppPeer, m *Message) {
	glog.Infoln("== BattleResult ==")
	glog.Infoln("ID:", p.User.UserId)
	glog.Infoln("Name:", p.User.Name)
	result := parseBattleResult(m)
	js, _ := json.MarshalIndent(result, "", "  ")
	glog.Infoln(string(js))
	glog.Infoln("==================")

	p.app.OnGetBattleResult(p, result)
})
