// ロビーへの入退出 チャットの送受信などのメッセージをハンドリング
package lobby

import (
	"fmt"
	. "zdxsv/pkg/lobby/message"
)

var _ = register(0x6203, "GetPlazaCount", func(p *AppPeer, m *Message) {
	id := m.Reader().Read16()
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(id)
	w.Write8(1) // TODO:調査
	p.SendMessage(a)
})

var _ = register(0x6207, "EnterPlaza", func(p *AppPeer, m *Message) {
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6205, "GetPlazaJoinUser", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	id := m.Reader().Read16()
	count := p.app.OnGetPlazaJoinUser()
	w.Write16(id)
	w.Write16(0)
	w.Write16(count) // 全体対戦中ユーザ数
	p.SendMessage(a)
})

var _ = register(0x6206, "GetPlazaStatus", func(p *AppPeer, m *Message) {
	r := m.Reader()
	id := r.Read16()
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(id)
	w.Write8(2) // TODO:調査
	p.SendMessage(a)
})

var _ = register(0x6301, "GetLobbyCount", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write(m.Body)
	w.Write8(0x06) // TODO:調査
	p.SendMessage(a)
})

var _ = register(0x6303, "GetLobbyUserCount", func(p *AppPeer, m *Message) {
	lobbyId := m.Reader().Read16()
	count := p.app.OnGetLobbyUserCount(p, lobbyId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(lobbyId)
	w.Write16(count)
	p.SendMessage(a)
})

var _ = register(0x6304, "GetLobbyUserStatus", func(p *AppPeer, m *Message) {
	lobbyId := m.Reader().Read16()
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(lobbyId)
	w.Write8(3) // 0:1:2:出入り不可 3:出入り可能
	p.SendMessage(a)
})

var _ = register(0x6308, "GetLobbyExplain", func(p *AppPeer, m *Message) {
	lobbyId := m.Reader().Read16()
	a := NewServerAnswer(m)
	w := a.Writer()

	if lobbyId == 1 {
		w.Write16(lobbyId)
		w.WriteString(fmt.Sprintf("<B>ロビー %d<BR>接続テスト対戦専用", lobbyId))
	} else if lobbyId == 3 {
		targetBodySize := 0x0120 - 8

		w.Write16(lobbyId)
		w.Write16(uint16(targetBodySize - 4))
		w.Write8('<')
		w.Write8('B')
		w.Write8('>')
		w.Write8('f')
		w.Write8('i')
		w.Write8('x')
		w.Write8('l')
		w.Write8('a')
		w.Write8('g')
		w.Write8('t')
		w.Write8('b')
		w.Write8('l')
		w.Write32(uint32(0))
		w.Write32(uint32(0))
		w.Write32(uint32(0))
		w.Write32(uint32(0))

		fixLagTable := []uint32{
			0x00000000, 0x00000000, 0x00000000, 0x00000000,
			0x27bdffb0,
			0xffa40040, 0xffa50030, 0xffa20020, 0xffa30010,
			0x24040002, 0x24050006, 0x3c030060, 0x2463fba0,
			0xa0640000, 0xa0650004, 0xa0650008,
			0xa064000c, 0xa0650010, 0xa0650014,
			0xa0640018, 0xa065001c, 0xa0650020,
			0xa0640024, 0xa0650028, 0xa065002c,
			0xa0640030, 0xa0650034, 0xa0650038,
			0xa064003c, 0xa0650040, 0xa0650044,
			0xdfa40040, 0xdfa50030, 0xdfa20020, 0xdfa30010,
			0x27bd0050,
		}

		for _, op := range fixLagTable {
			w.Write32LE(op)
		}

		// return to original address, fixing sp.
		w.Write32LE(uint32(0xdfbf0000)) // ld ra $0000(sp)
		w.Write32LE(uint32(0x00000000)) // nop
		w.Write32LE(uint32(0x27bd0010)) // addiu sp, sp $0010
		w.Write32LE(uint32(0x00000000)) // nop
		w.Write32LE(uint32(0x03e00008)) // jr ra

		for w.BodyLen() < targetBodySize-8 {
			w.Write8(uint8(0))
		}

		// Reproduce client stack.
		w.Write16LE(0)
		w.Write16LE(lobbyId)

		// Overwrite return addr in stack for client to run my program.
		w.Write32LE(uint32(0x00c22cc0))
	} else {
		w.Write16(lobbyId)
		w.WriteString(fmt.Sprintf("<B>ロビー %d<B>", lobbyId))
	}
	p.SendMessage(a)
})

var _ = register(0x6305, "EnterLobby", func(p *AppPeer, m *Message) {
	lobbyId := m.Reader().Read16()
	a := NewServerAnswer(m)
	p.app.OnEnterLobby(p, lobbyId)
	p.SendMessage(a)

})

var _ = register(0x6408, "ExitLobby", func(p *AppPeer, m *Message) {
	p.app.OnExitLobby(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6208, "TopPageJump", func(p *AppPeer, m *Message) {
	p.app.OnUserTopPageJump(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x640F, "GetLobbyEntryUserCount", func(p *AppPeer, m *Message) {
	lobbyId := m.Reader().Read16()
	aeug, titans := p.app.OnGetLobbyEntryUserCount(p, lobbyId)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(lobbyId)
	w.Write16(aeug)
	w.Write16(titans)
	p.SendMessage(a)
})

var _ = register(0x640E, "EntryLobbyBattle", func(p *AppPeer, m *Message) {
	side := m.Reader().Read8()
	p.app.OnEntryLobbyBattle(p, side)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6707, "GetFrendOnline", func(p *AppPeer, m *Message) {
	_ = m.Reader().ReadEncryptedString() // ユーザID
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(0x0000) // よくわからん
	p.SendMessage(a)
})

var _ = register(0x6703, "FindFrendStatus", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	// string
	p.SendMessage(a)
})

var _ = register(0x6704, "SendMailMessage", func(p *AppPeer, m *Message) {
	r := m.Reader()
	_ = r.ReadEncryptedString() // ユーザID
	_ = r.ReadEncryptedString() // メッセージ
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(0x0001) // よくわからん
	p.SendMessage(a)
})

var _ = register(0x6701, "SendChatMessage", func(p *AppPeer, m *Message) {
	str := m.Reader().ReadEncryptedString()
	p.app.OnSendChatMessage(p, str)
})

// TODO
func NoticeBothGameJoinUser() {
	_ = NewServerNotice(0x6202)
}

func NoticeBothPlazaJoinUser(p *AppPeer, id uint16, count uint16) {
	n := NewServerNotice(0x6205)
	w := n.Writer()
	w.Write16(id)
	w.Write16(0)
	w.Write16(count) // 全体対戦中ユーザ数
	p.SendMessage(n)
}

func NoticeChatMessage(p *AppPeer, userId, name, message string) {
	n := NewServerNotice(0x6702)
	w := n.Writer()
	w.WriteString(userId)
	w.WriteString(name)
	w.WriteString(message)
	p.SendMessage(n)
}

func NoticeLobbyUserCount(p *AppPeer, lobbyId, inLobby, inBattle uint16) {
	n := NewServerNotice(0x6303)
	w := n.Writer()
	w.Write16(lobbyId)
	w.Write16(inLobby)
	w.Write16(inBattle)
	p.SendMessage(n)
}

func NoticeEntryUserCount(p *AppPeer, lobbyId, aeug, titans uint16) {
	// Doesn't work..
	n := NewServerNotice(0x640F)
	w := n.Writer()
	w.Write16(lobbyId)
	w.Write16(aeug)
	w.Write16(titans)
	p.SendMessage(n)
}
