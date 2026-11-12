package main

import (
	"fmt"

	"go.uber.org/zap"
)

// 6007 : ServerFull サーバ選択画面へ
// 6003 : 強制ログイン画面へ メンテに使えるかも
//

var _ = register(0x6006, "Logout", func(p *LbsPeer, m *LbsMessage) {
	p.app.OnUserLogout(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6002, "GotoBattle", func(p *LbsPeer, m *LbsMessage) {
	p.app.OnUserGotoBattle(p)
	p.SendMessage(NewServerAnswer(m))
})

func SendServerShutDown(p *LbsPeer) {
	n := NewServerNotice(0x6003)
	w := n.Writer()
	w.WriteString("<BODY><LF=6><CENTER>サーバがシャットダウンしました<END>")
	p.SendMessage(n)
}

// maxMachineIDLen bounds what a client can store as its machine id.
const maxMachineIDLen = 64

func SendBanned(p *LbsPeer) {
	n := NewServerNotice(0x6003)
	n.Writer().WriteString("<BODY><LF=5><CENTER>YOU ARE BANNED<END>")
	p.SendMessage(n)
}

func SendPleaseUpdate(p *LbsPeer) {
	n := NewServerNotice(0x6003)
	n.Writer().WriteString("<BODY><LF=5><CENTER>PLEASE UPDATE PCSX2<END>")
	p.SendMessage(n)
}

func RequestLineCheck(p *LbsPeer) {
	m := NewServerQuestion(0x6001)
	p.SendMessage(m)
}

var _ = register(0x6001, "OnLineCheck", func(_ *LbsPeer, _ *LbsMessage) {
})

var _ = register(0x600E, "EchoPacket", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write(m.Body)
	p.SendMessage(a)
})

var _ = register(0x61A0, "NAZO 0x61A0", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	w := a.Writer()
	// 自動切断猶予
	w.Write([]byte{0x00, 0x00, 0x0E, 0x10, 0x00, 0x00, 0x02, 0x58})
	p.SendMessage(a)
})

// Login Sequence
func RequestKeyPair(p *LbsPeer) {
	m := NewServerQuestion(0x6101)
	m.Seq = 1
	w := m.Writer()
	w.Write16(0x2837)
	p.SendMessage(m)
}

// PlatformInfo is sent only by emulators, before the key pair answer.
// Custom category, no answer: the game never sees it.
var _ = register(0x9950, "PlatformInfo", func(p *LbsPeer, m *LbsMessage) {
	p.app.OnPlatformInfo(p, ParsePlatformInfo(string(m.Body)))
})

var _ = register(0x6101, "ResponseKeyPair", func(p *LbsPeer, m *LbsMessage) {
	r := m.Reader()
	key1 := r.ReadString()
	loginKey := r.ReadEncryptedString()
	logger.Info("login key", zap.Any("key1", key1))
	sessionID, err := DecodeSessionID(key1)
	if err != nil {
		logger.Error("login failed", zap.Error(err))
		return
	}
	p.app.OnKeePair(p, loginKey, sessionID)
})

func RequestFirstData(p *LbsPeer) {
	m := NewServerQuestion(0x6103)
	p.SendMessage(m)
}

var _ = register(0x6103, "FirstData", func(p *LbsPeer, _ *LbsMessage) {
	p.app.OnFirstData(p)
})

func NoticeUserIDList(p *LbsPeer, users []*DBUser) {
	m := NewServerNotice(0x6131)
	w := m.Writer()
	w.Write8(byte(len(users)))
	for _, u := range users {
		w.WriteString(u.UserID)
		w.WriteString(u.Name)
		w.WriteString(u.Team)
	}
	p.SendMessage(m)
}

var _ = register(0x6132, "DecideUserID", func(p *LbsPeer, m *LbsMessage) {
	r := m.Reader()
	userID := r.ReadEncryptedString()
	name := r.ReadEncryptedString()
	if !p.app.OnDecideUserID(p, userID, name) {
		return
	}
	a := NewServerAnswer(m)
	w := a.Writer()
	w.WriteString(p.UserID)
	p.SendMessage(a)
})

var _ = register(0x6190, "DecideTeam", func(p *LbsPeer, m *LbsMessage) {
	team := m.Reader().ReadEncryptedString()
	p.app.OnDecideTeam(p, team)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x614C, "GetTopInformation", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	// 0: no カプコン情報 window. 1-3 show it to a wider audience each and
	// must be followed by the text, e.g. "<BODY><LF=5><CENTER>...<END>".
	a.Writer().Write8(0)
	p.SendMessage(a)
})

func NoticeLoginOk(p *LbsPeer) {
	p.SendMessage(NewServerNotice(0x6104))
}

var _ = register(0x6144, "GetParsonalRecordHeader", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	w := a.Writer()
	// The response data is all 1 byte size,
	// the first one seems to represent the number that follows.
	w.Write8(0)
	p.SendMessage(a)
})

var _ = register(0x6145, "GetParsonalRecordData", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	r := m.Reader()
	kind := r.Read8()
	page := r.Read8()

	rec := p.app.OnGetUserRanking(p, kind, page)

	w := a.Writer()
	w.Write8(kind)
	w.Write8(page)
	w.Write32(rec.EntireCount)
	w.Write32(0xAAAA) // Unknown
	w.Write32(rec.Invalid)
	w.Write8(rec.Class)
	w.Write32(rec.Rank)
	w.Write32(0xBBBB) // Unknown
	w.Write32(rec.Battle)
	w.Write32(rec.Win)
	w.Write32(rec.Lose)
	w.Write32(rec.Kill)
	w.Write32(0xCCCC) // Unknown
	w.Write32(0xDDDD) // Unknown
	w.Write32(0xEEEE) // Unknown
	w.Write32(0xFFFF) // Unknown

	p.SendMessage(a)
})

// *UNUSED*
var _ = register(0x6146, "GetParsonalRecordVide", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	r := m.Reader()
	kind := r.Read8()
	page := r.Read8()

	w := a.Writer()
	w.Write8(kind)
	w.Write8(page)

	w.WriteString(fmt.Sprintf("Vide %v %v", kind, page))
	w.Write8(1)
	w.Write8(2)
	w.Write8(3)
	w.Write8(4)
	w.Write8(5)
	w.Write8(6)
})

var _ = register(0x6143, "SetUserBinary", func(p *LbsPeer, m *LbsMessage) {
	r := m.Reader()
	bin := r.ReadEncryptedString()
	p.app.OnSetUserBinary(p, bin)
	p.SendMessage(NewServerAnswer(m))
})

// 0x6143のレスポンスを送ったらクライアントが送ってくる.
// 0バイトだから特に情報はないが, 目的は不明.
var _ = register(0x6141, "NoticeNazo", func(p *LbsPeer, m *LbsMessage) {
})

// Custom API
var _ = register(0x0001, "RegisterOldProxy", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	a.Category = CategoryCustom
	a.Writer().WriteString("OLD VERSION")
	p.SendMessage(a)
})
