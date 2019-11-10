// ロビーへの入退出 チャットの送受信などのメッセージをハンドリング
package lobby

import (
	"fmt"
	"io"
	"strings"
	. "zdxsv/pkg/lobby/message"

	"github.com/golang/glog"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

var _ = register(0x6203, "GetPlazaCount", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(25) // Not sure
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
	w.Write16(25) // Not sure
	p.SendMessage(a)
})

var _ = register(0x6303, "GetLobbyUserCount", func(p *AppPeer, m *Message) {
	lobbyID := m.Reader().Read16()
	count := p.app.OnGetLobbyUserCount(p, lobbyID)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(lobbyID)
	w.Write16(count)
	p.SendMessage(a)
})

var _ = register(0x6304, "GetLobbyUserStatus", func(p *AppPeer, m *Message) {
	lobbyID := m.Reader().Read16()
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(lobbyID)
	w.Write8(3) // 0:1:2:出入り不可 3:出入り可能
	p.SendMessage(a)
})

var _ = register(0x6308, "GetLobbyExplain", func(p *AppPeer, m *Message) {
	lobbyID := m.Reader().Read16()
	a := NewServerAnswer(m)
	w := a.Writer()
	switch lobbyID {
	case 1:
		w.Write16(lobbyID)
		w.WriteString(fmt.Sprintf("<B>Lobby %02d<BR>接続テスト対戦専用<END>", lobbyID))
	case 3:
		a = SetPadDelayLobbyHack(p, m)
	case 4:
		a = SetWideScreenLobbyHack(p, m)
	default:
		w.Write16(lobbyID)
		w.WriteString(fmt.Sprintf("<B>Lobby %02d<END>", lobbyID))
	}
	p.SendMessage(a)
})

var _ = register(0x6305, "EnterLobby", func(p *AppPeer, m *Message) {
	lobbyID := m.Reader().Read16()
	a := NewServerAnswer(m)
	p.app.OnEnterLobby(p, lobbyID)
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
	lobbyID := m.Reader().Read16()
	aeug, titans := p.app.OnGetLobbyEntryUserCount(p, lobbyID)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(lobbyID)
	w.Write16(aeug)
	w.Write16(titans)
	p.SendMessage(a)

	if lobbyID == 1 {
		SendLobbyChatHackNotice(p)
	}
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
	if p.Lobby != nil && p.Lobby.ID == 1 {
		LobbyChatHack(p, str)
	}
	p.app.OnSendChatMessage(p, str)
})

var _ = register(0x6801, "GetRegurationData", func(p *AppPeer, m *Message) {
	str := m.Reader().ReadEncryptedString()
	glog.Infoln("str", str)
	// 06/INFOR/INFOR00.HTM   : インフォメーション
	// 06/V_RANK/TOTAL00.HTM  : 通信対戦ランキング > 総合
	// 06/V_RANK/AEUG00.HTM   : 通信対戦ランキング > 連邦エゥーゴ
	// 06/V_RANK/TITANS00.HTM : 通信対戦ランキング > ジオンティターンズ
	// 06/P_RANK/TOTAL00.HTM  : 撃墜数ランキング > 総合
	// 06/P_RANK/AEUG00.HTM   : 撃墜数ランキング > 連邦エゥーゴ
	// 06/P_RANK/TITANS00.HTM : 撃墜数ランキング > ジオンティターンズ
	// 06/E_RANK/TOTAL00.HTM  : イベントランキング > 総合
	// 06/E_RANK/AEUG00.HTM   : イベントランキング > 連邦エゥーゴ
	// 06/E_RANK/TITANS00.HTM : イベントランキング > ジオンティターンズ

	m.Writer()
	a := NewServerAnswer(m)
	content := `
	<HTML>
	<HEAD>
		<TITLE> UNDER CONSTRUCTION </TITLE>
	</HEAD>
	<!--
		<GAME-STYLE>
			"MOUSE=OFF",
			"SCROLL=OFF",
			"TITLE=OFF",
			"BACK=ON:afs://02/8",
			"FORWARD=OFF",
			"CANCEL=OFF",
			"RELOAD=OFF",
			"CHOICE_MV=OFF",
			"X_SHOW=ON",
			"LINK_U=OFF",
		</GAME-STYLE>
	-->
	<BODY BGCOLOR=#000000 background=afs://02/114.PNG text=white link=white vlink=white>
	
	<TABLE WIDTH=584 CELLSPACING=0 CELLPADDING=0>
	
	<!-- タイトル -->
	 <TR>
	  <TD BACKGROUND=afs://02/121.PNG WIDTH=256 HEIGHT=44>
	  <TD BACKGROUND=afs://02/122.PNG WIDTH=32  HEIGHT=44>
	  <TD BACKGROUND=afs://02/123.PNG WIDTH=296 HEIGHT=44 ALIGN=RIGHT><font size=1>　<br></font>工事中　　　　　
	 </TR>
	
	 <TR><TD COLSPAN=3>
	
	<!-- 項目 -->
	<CENTER>
	<FONT SIZE=5>
	</FONT>
	</CENTER>
	
	</BODY>
	</HTML>
`
	io.Copy(a.Writer(), transform.NewReader(strings.NewReader(content), japanese.EUCJP.NewEncoder()))
	p.SendMessage(a)
})

func NoticeBothPlazaJoinUser(p *AppPeer, id uint16, count uint16) {
	n := NewServerNotice(0x6205)
	w := n.Writer()
	w.Write16(id)
	w.Write16(0)
	w.Write16(count) // 全体対戦中ユーザ数
	p.SendMessage(n)
}

func NoticeChatMessage(p *AppPeer, userID, name, message string) {
	n := NewServerNotice(0x6702)
	w := n.Writer()
	w.WriteString(userID)
	w.WriteString(name)
	w.WriteString(message)
	p.SendMessage(n)
}

func NoticeLobbyUserCount(p *AppPeer, lobbyID, inLobby, inBattle uint16) {
	n := NewServerNotice(0x6303)
	w := n.Writer()
	w.Write16(lobbyID)
	w.Write16(inLobby)
	w.Write16(inBattle)
	p.SendMessage(n)
}

// NoticeLobbyEntryUserCount reinforms the peer about
// how many players entry to lobby match in the lobby.
func NoticeLobbyEntryUserCount(p *AppPeer, lobbyID, aeug, titans uint16) {
	n := NewServerNotice(0x640F)
	w := n.Writer()
	w.Write16(lobbyID)
	w.Write16(aeug)
	w.Write16(titans)
	p.SendMessage(n)
}
