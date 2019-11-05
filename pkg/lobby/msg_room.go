package lobby

import (
	. "zdxsv/pkg/lobby/message"

	"github.com/golang/glog"
)

var _ = register(0x6401, "GetRoomCount", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	count := p.app.OnGetRoomCount(p)
	w := a.Writer()
	w.Write16(count) // 部屋数
	p.SendMessage(a)
})

var _ = register(0x6402, "GetRoomName", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	name := p.app.OnGetRoomName(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(roomId)
	w.WriteString(name)
	p.SendMessage(a)
})

var _ = register(0x640B, "GetRoomJoinInfo", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	max := p.app.OnGetRoomJoinInfo(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(roomId)
	w.Write16(max) // 最大人数
	w.Write16(0)
	w.Write16(0)      //
	w.Write16(0xFFFF) // 最大参加人数?
	w.Write16(0)      // 対戦開始押せるか 0:押せる 1:押せない
	p.SendMessage(a)
})

var _ = register(0x6403, "GetRoomUserCount", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	count := p.app.OnGetRoomUserCount(p, roomId)
	maxCount := p.app.OnGetRoomJoinInfo(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(roomId)
	w.Write16(count) // 現在人数
	// なんかこの後にWrite16したらティターンズの人数が変わったがメモリ的な問題っぽい
	w.Write16(0) // ???
	w.Write16(maxCount)
	w.Write16(count) // ???
	p.SendMessage(a)
})

var _ = register(0x6404, "GetRoomStatus", func(p *AppPeer, m *Message) {
	// 0:not avaibale X この部屋は使用できません
	// 1:empty room 空き
	// 2:prepareing 準備中
	// 3:recruiting 募集中
	// 4:full 満員 満室のため入室できません
	// 5:fulled X 定員が埋まりました
	roomId := m.Reader().Read16()
	status := p.app.OnGetRoomStatus(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(roomId)
	w.Write8(status)
	p.SendMessage(a)
})

var _ = register(0x6405, "GetRoomPasswordInfo", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	pass, ok := p.app.OnGetRoomPasswordInfo(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(roomId)
	if !ok {
		w.Write8(0x00) // 0: パスワードなし
	} else {
		w.Write8(0x01)
		_ = pass //TODO
	}
	p.SendMessage(a)
})

var _ = register(0x6407, "RequestCreateRoom", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	ok := p.app.OnRequestCreateRoom(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	if ok {
		w.Write16(roomId)
	} else {
		a.Status = StatusError
		w.WriteString("<B>エラー<B> ")
	}
	p.SendMessage(a)
})

var _ = register(0x6603, "GetRuleCount", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	count := p.app.OnRequestGetRuleCount(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(count) // ルール数
	p.SendMessage(a)
})

var _ = register(0x6601, "GetNamePermission", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	result := p.app.OnGetNamePermission(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(result)
	p.SendMessage(a)
})

var _ = register(0x6602, "GetPasswordPermission", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	result := p.app.OnGetPasswordPermission(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(result)
	p.SendMessage(a)
})

var _ = register(0x6604, "GetRuleName", func(p *AppPeer, m *Message) {
	r := m.Reader()
	roomId := r.Read16()
	ruleId := r.Read8()
	name := p.app.OnGetRuleName(p, roomId, ruleId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(ruleId)
	w.WriteString(name)
	p.SendMessage(a)
})

var _ = register(0x6605, "GetRulePermission", func(p *AppPeer, m *Message) {
	r := m.Reader()
	roomId := r.Read16()
	ruleId := r.Read8()
	result := p.app.OnGetRulePermission(p, roomId, ruleId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(ruleId)
	w.Write8(result) //1:ルール設定可能
	p.SendMessage(a)
})

var _ = register(0x6606, "GetRuleDefaultIndex", func(p *AppPeer, m *Message) {
	r := m.Reader()
	roomId := r.Read16()
	ruleId := r.Read8()
	index := p.app.OnGetRuleDefaultIndex(p, roomId, ruleId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(ruleId)
	w.Write8(index)
	p.SendMessage(a)
})

var _ = register(0x6608, "GetRuleElementName", func(p *AppPeer, m *Message) {
	r := m.Reader()
	roomId := r.Read16()
	ruleId := r.Read8()
	elemId := r.Read8()
	name := p.app.OnGetRuleElementName(p, roomId, ruleId, elemId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(ruleId)
	w.Write8(elemId)
	w.WriteString(name)
	p.SendMessage(a)
})

var _ = register(0x660E, "GetRuleControl", func(p *AppPeer, m *Message) {
	r := m.Reader()
	roomId := r.Read16()
	ruleId := r.Read8()
	elemId := r.Read8()
	result := p.app.OnGetRuleControl(p, roomId, ruleId, elemId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(ruleId)
	w.Write8(elemId)
	w.Write8(result)
	p.SendMessage(a)
})

var _ = register(0x6607, "GetRuleElementCount", func(p *AppPeer, m *Message) {
	r := m.Reader()
	roomId := r.Read16()
	ruleId := r.Read8()
	count := p.app.OnGetRuleElementCount(p, roomId, ruleId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(ruleId)
	w.Write8(count)
	p.SendMessage(a)
})

var _ = register(0x6609, "DecideRoomName", func(p *AppPeer, m *Message) {
	r := m.Reader()
	name := r.ReadEncryptedString()
	p.app.OnDecideRoomName(p, name)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x660A, "DecideRoomPassword", func(p *AppPeer, m *Message) {
	r := m.Reader()
	pass := r.ReadEncryptedString()
	p.app.OnDecideRoomPassword(p, pass)
	glog.Infoln(pass)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x660B, "DecideRule", func(p *AppPeer, m *Message) {
	r := m.Reader()
	ruleId := r.Read8()
	elemId := r.Read8()
	nazo := p.app.OnDecideRule(p, ruleId, elemId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(nazo)
	p.SendMessage(a)
})

var _ = register(0x660C, "DecideRuleFinish", func(p *AppPeer, m *Message) {
	p.app.OnDecideRuleFinish(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6409, "GetRoomRestTime", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	a := NewServerAnswer(m)
	sec := p.app.OnGetRoomRestTime(p, roomId)
	w := a.Writer()
	w.Write16(roomId)
	w.Write16(sec)
	p.SendMessage(a)
})

var _ = register(0x640A, "GetRoomMember", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	count, users := p.app.OnGetRoomMember(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(roomId)
	w.Write16(count)
	for _, u := range users {
		w.WriteString(u)
	}
	p.SendMessage(a)
})

var _ = register(0x6413, "GetRoomEntryList", func(p *AppPeer, m *Message) {
	roomId := m.Reader().Read16()
	count, ids, sides := p.app.OnGetRoomEntryList(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(roomId)
	w.Write8(count)
	for i := 0; i < int(count); i++ {
		w.WriteString(ids[i]) // ユーザID
		w.Write8(sides[i])    // エントリーサイド
	}
	p.SendMessage(a)
})

var _ = register(0x6504, "EntryRoomMatch", func(p *AppPeer, m *Message) {
	side := m.Reader().Read8()
	p.app.OnEntryRoomMatch(p, side)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(side)
	p.SendMessage(a)
})

var _ = register(0x6406, "EnterRoom", func(p *AppPeer, m *Message) {
	r := m.Reader()
	roomId := r.Read16()
	unk1 := r.Read16()
	unk2 := r.Read16()
	p.app.OnEnterRoom(p, roomId, unk1, unk2)
	glog.Infoln("EnterRoom", roomId, unk1, unk2)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6501, "ExitRoom", func(p *AppPeer, m *Message) {
	p.app.OnExitRoom(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6412, "GetRoomMatchEntryUserCount", func(p *AppPeer, m *Message) {
	r := m.Reader()
	roomId := r.Read16()
	aeug, titans := p.app.OnGetRoomMatchEntryUserCount(p, roomId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(roomId)
	w.Write16(aeug)
	w.Write16(titans)
	p.SendMessage(a)
})

var _ = register(0x6508, "NoticeRoomBattleStart", func(p *AppPeer, m *Message) {
	p.app.OnNoticeRoomBattleStart(p)
})

func NoticeRoomEntry(p *AppPeer, roomId uint16, userId string, side byte) {
	n := NewServerNotice(0x6414)
	w := n.Writer()
	w.Write16(roomId)
	w.WriteString(userId)
	w.Write8(side)
	p.SendMessage(n)
}

func NoticeRoomName(p *AppPeer, roomId uint16, name string) {
	n := NewServerNotice(0x6402)
	w := n.Writer()
	w.Write16(roomId)
	w.WriteString(name)
	p.SendMessage(n)
}

func NoticeRoomEntryUserCountForLobbyUser(p *AppPeer, roomId, aeug, titans uint16) {
	n := NewServerNotice(0x6412)
	w := n.Writer()
	w.Write16(roomId)
	w.Write16(aeug)
	w.Write16(titans)
	p.SendMessage(n)
}

func NoticeRoomStatus(p *AppPeer, roomId uint16, status byte) {
	n := NewServerNotice(0x6404)
	w := n.Writer()
	w.Write16(roomId)
	w.Write8(status)
	p.SendMessage(n)
}

func NoticeRoomUserCount(p *AppPeer, roomId, count uint16) {
	n := NewServerNotice(0x6403)
	w := n.Writer()
	w.Write16(roomId)
	w.Write16(count)
	p.SendMessage(n)
}

func NoticeRoomJoinInfo(p *AppPeer, roomId, max uint16) {
	n := NewServerNotice(0x640B)
	w := n.Writer()
	w.Write16(roomId)
	w.Write16(max)
	w.Write16(0) // ティターンズの参加人数表示, 一旦0固定
	w.Write16(0)
	w.Write16(0xFFFF) // 参加できるか?
	w.Write16(0)
	p.SendMessage(n)
}

func NoticeExitRoom(p *AppPeer, userId, name, team string) {
	n := NewServerNotice(0x6502)
	w := n.Writer()
	w.WriteString(userId)
	w.WriteString(name)
	w.WriteString(team)
	p.SendMessage(n)
}

func NoticeJoinRoom(p *AppPeer, userId, name, team string) {
	n := NewServerNotice(0x6503)
	w := n.Writer()
	w.WriteString(userId)
	w.WriteString(name)
	w.WriteString(team)
	p.SendMessage(n)
}

func NoticeRemoveRoom(p *AppPeer) {
	n := NewServerNotice(0x6505)
	w := n.Writer()
	w.WriteString("<BODY><LF=6><CENTER>部屋が解散になりました。<END>")
	p.SendMessage(n)
}
