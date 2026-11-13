package main

import (
	"fmt"
)

const roomCount = 5

const LobbyCount = 26

type LbsLobby struct {
	ID         uint16
	Platform   string
	Game       string
	Rule       *Rule
	Start      string
	Users      map[string]*LbsUser
	Rooms      map[uint16]*LbsRoom
	EntryUsers []string
}

func NewLobby(platform string, lobbyID uint16) *LbsLobby {
	lobby := &LbsLobby{
		ID:         lobbyID,
		Platform:   platform,
		Rule:       NewRule(),
		Users:      make(map[string]*LbsUser),
		Rooms:      make(map[uint16]*LbsRoom),
		EntryUsers: make([]string, 0),
	}
	for i := uint16(0); i <= roomCount; i++ {
		lobby.Rooms[i] = NewRoom(lobbyID, i)
	}
	return lobby
}

func (l *LbsLobby) RoomCount() uint16 {
	return uint16(roomCount)
}

func (l *LbsLobby) Enter(u *LbsUser) {
	l.Users[u.UserID] = u
}

func (l *LbsLobby) Exit(userID string) {
	_, ok := l.Users[userID]
	if ok {
		delete(l.Users, userID)
		for i, id := range l.EntryUsers {
			if id == userID {
				l.EntryUsers = append(l.EntryUsers[:i], l.EntryUsers[i+1:]...)
				break
			}
		}
	}
}

func (l *LbsLobby) Entry(u *LbsUser, side byte) {
	u.Entry = side
	if side == EntryNone {
		for i, id := range l.EntryUsers {
			if id == u.UserID {
				l.EntryUsers = append(l.EntryUsers[:i], l.EntryUsers[i+1:]...)
				break
			}
		}
	} else {
		l.EntryUsers = append(l.EntryUsers, u.UserID)
	}
}

func (l *LbsLobby) GetEntryUserCount() (uint16, uint16) {
	a := uint16(0)
	b := uint16(0)
	for _, id := range l.EntryUsers {
		u, ok := l.Users[id]
		if ok {
			switch u.Entry {
			case EntryAeug:
				a++
			case EntryTitans:
				b++
			}
		}
	}
	return a, b
}

func (l *LbsLobby) CanBattleStart() bool {
	_, ok := l.startGroup()
	return ok
}

// Start values: the players per side a lobby battle needs. Empty: 2v2.
const (
	Start2v2  = "2v2"
	Start1v1  = "1v1"
	StartSolo = "solo"
)

func (l *LbsLobby) canStart(a, b int) bool {
	switch l.Start {
	case Start1v1:
		return 1 <= a && 1 <= b
	case StartSolo:
		return 1 <= a || 1 <= b
	}
	return 2 <= a && 2 <= b
}

// startGroup returns the sync fingerprint of the first entry group (in entry order)
// that can start a battle, else the first entry user's and false.
func (l *LbsLobby) startGroup() (string, bool) {
	first, seen := "", false
	counts := map[string]*[2]int{}
	for _, id := range l.EntryUsers {
		u, ok := l.Users[id]
		if !ok {
			continue
		}
		if !seen {
			first, seen = u.Sync, true
		}
		c := counts[u.Sync]
		if c == nil {
			c = &[2]int{}
			counts[u.Sync] = c
		}
		switch u.Entry {
		case EntryAeug:
			c[0]++
		case EntryTitans:
			c[1]++
		}
		if l.canStart(c[0], c[1]) {
			return u.Sync, true
		}
	}
	return first, false
}

// StartBattleUsers picks up to two users per side from the startGroup.
func (l *LbsLobby) StartBattleUsers() []*LbsUser {
	sync, _ := l.startGroup()
	a := uint16(0)
	b := uint16(0)
	ret := []*LbsUser{}
	for _, id := range l.EntryUsers {
		u, ok := l.Users[id]
		if ok && u.Sync == sync {
			switch u.Entry {
			case EntryAeug:
				if a < 2 {
					ret = append(ret, u)
				}
				a++
			case EntryTitans:
				if b < 2 {
					ret = append(ret, u)
				}
				b++
			}
		}
	}
	return ret
}

// Hack values: the special uses of a lobby, comma separated. Empty: none.
const (
	HackNone        = "none"
	HackPadDelay    = "pad_delay"
	HackWideScreen  = "widescreen"
	HackZproxyTest  = "zproxy_test"
	HackChatCommand = "chat_command"
)

func SendLobbyChatHackNotice(p *LbsPeer, s *MLobbySetting) {
	if lobbyHasHack(s, HackZproxyTest) {
		NoticeChatMessage(p, "SERVER", "＞", "自動選抜に入ることでＵＤＰプロキシの")
		NoticeChatMessage(p, "SERVER", "＞", "接続テスト対戦を行うことができます。")
	}
	if !lobbyHasHack(s, HackChatCommand) {
		return
	}
	var cmds []string
	if platformHasWideScreen(p.Platform) {
		cmds = append(cmds, "ｗ０：ワイドスクリーンパッチ無効化", "ｗ１：ワイドスクリーンパッチ有効化")
	}
	if p.app.carryCode != nil {
		cmds = append(cmds, "ｄｃ：エミュレーター用引き継ぎコード")
	}
	if len(cmds) == 0 {
		return
	}
	NoticeChatMessage(p, "SERVER", "＞", "利用可能コマンド")
	for _, c := range cmds {
		NoticeChatMessage(p, "SERVER", "＞", c)
	}
}

func LobbyChatHack(p *LbsPeer, str string) {
	if (str != "ｗ０" && str != "ｗ１") || !platformHasWideScreen(p.Platform) {
		return
	}
	switch str {
	case "ｗ０":
		p.System = (p.System & uint32(0xffffff00)) | 0x00
		NoticeChatMessage(p, "SERVER", "＞", "ワイドスクリーンパッチを無効化")
		NoticeChatMessage(p, "SERVER", "＞", "戦場選択画面に戻ってください")
		NoticeChatMessage(p, "SERVER", "＞", "この設定はユーザ別に保存されます")
		DefaultDB.UpdateUser(&p.LbsUser.DBUser)
	case "ｗ１":
		p.System = (p.System & uint32(0xffffff00)) | 0x01
		NoticeChatMessage(p, "SERVER", "＞", "ワイドスクリーンパッチを有効化")
		NoticeChatMessage(p, "SERVER", "＞", "戦場選択画面に戻ってください")
		NoticeChatMessage(p, "SERVER", "＞", "この設定はユーザ別に保存されます")
		DefaultDB.UpdateUser(&p.LbsUser.DBUser)
	}
}

// SetPadDelayLobbyHack writes answer of LobbyExplain message
// with a function that sets pad delay to static value.
func SetPadDelayLobbyHack(p *LbsPeer, m *LbsMessage) *LbsMessage {
	lobbyID := m.Reader().Read16()
	a := NewServerAnswer(m)
	w := a.Writer()

	targetBodySize := 0x0120 - 8

	w.Write16(lobbyID)
	w.Write16(uint16(targetBodySize - 4))

	w.Write([]byte(fmt.Sprintf("<B>Lobby %02d<END>", lobbyID)))
	w.Write32(uint32(0))
	w.Write32(uint32(0))
	w.Write32(uint32(0))

	// R5900 Function: Fill pad delay table
	// (initial, soft_limit, hard_limit) * 6 to static value.
	for _, op := range []uint32{
		0x27bdffb0, // sp -= 0x0050

		0xffa40040, 0xffa50030, 0xffa20020, 0xffa30010, // save a0, a1, v0, v1 to stack
		0x24040002, 0x24050005, 0x3c030060, 0x2463fba0, // a0 = 2, a1 = 5, v1 = 0x005ffba0(table)

		0xa0640000, 0xa0650004, 0xa0650008, // table[0] = (a0, a1, a1)
		0xa064000c, 0xa0650010, 0xa0650014, // table[1] = (a0, a1, a1)
		0xa0640018, 0xa065001c, 0xa0650020, // table[2] = (a0, a1, a1)
		0xa0640024, 0xa0650028, 0xa065002c, // table[3] = (a0, a1, a1)
		0xa0640030, 0xa0650034, 0xa0650038, // table[4] = (a0, a1, a1)
		0xa064003c, 0xa0650040, 0xa0650044, // table[5] = (a0, a1, a1)

		0xdfa40040, 0xdfa50030, 0xdfa20020, 0xdfa30010, // load a0, a1, v0, v1 from stack

		0x27bd0050, // sp += 0x0050
	} {
		w.Write32LE(op)
	}

	// return to original address, fixing sp.
	w.Write32LE(uint32(0xdfbf0000)) // ld ra $0000(sp)
	w.Write32LE(uint32(0x03e00008)) // jr ra
	w.Write32LE(uint32(0x27bd0010)) // addiu sp, sp $0010

	// padding
	for w.BodyLen() < targetBodySize-8 {
		w.Write8(uint8(0))
	}

	// Reproduce client stack.
	w.Write16LE(0)
	w.Write16LE(lobbyID)

	// Overwrite return addr in stack for client to run the function.
	w.Write32LE(uint32(0x00c22cc0))

	return a
}

// SetWideScreenLobbyHack writes answer of LobbyExplain message
// with a function that sets widescreen patch.
func SetWideScreenLobbyHack(p *LbsPeer, m *LbsMessage) *LbsMessage {
	lobbyID := m.Reader().Read16()
	a := NewServerAnswer(m)
	w := a.Writer()

	targetBodySize := 0x0120 - 8

	w.Write16(lobbyID)
	w.Write16(uint16(targetBodySize - 4))

	w.Write([]byte(fmt.Sprintf("<B>Lobby %02d<END>", lobbyID)))
	w.Write32(uint32(0))
	w.Write32(uint32(0))
	w.Write32(uint32(0))

	fixvalue01 := uint32(0x24843f40)
	fixvalue02 := uint32(0x3c0444c0)
	fixvalue03 := uint32(0x2485f400)
	fixvalue04 := uint32(0x3c044440)
	fixvalue05 := uint32(0x2485e7ff)

	if p.System&0x0f == 0 {
		// set original value to disable the patch
		fixvalue01 = uint32(0x24843f80)
		fixvalue02 = uint32(0x3c044500)
		fixvalue03 = uint32(0x2485f000)
		fixvalue04 = uint32(0x3c044480)
		fixvalue05 = uint32(0x2485e000)
	}

	// apply wide screen patch.
	for _, op := range []uint32{
		0x27bdffb0, 0xffa40040, 0xffa50030, 0xffa20020,
		0xffa30010, 0xffbf0000, 0x3c020027, 0x3c043c02,
		fixvalue01, 0xac44cf84, 0x3c020084, fixvalue02,
		fixvalue03, 0xac453d30, fixvalue04, fixvalue05,
		0xac453ef0, 0xac4540b0, 0xdfa40040, 0xdfa50030,
		0xdfa20020, 0xdfa30010, 0xdfbf0000, 0x03e00008,
		0x27bd0050, 0x00000000, 0x27bdffb0, 0xffa40040,
		0xffa50030, 0xffa20020, 0xffa30010, 0xffbf0000,
		0x3c0500c2, 0x24a52cc0, 0x3c040010, 0x2484a000,
		0x3c060000, 0x0c046a66, 0x24c60064, 0x3c040c04,
		0x2484e800, 0x3c050027, 0xaca4d5d4, 0xdfa40040,
		0xdfa50030, 0xdfa20020, 0xdfa30010, 0xdfbf0000,
		0x27bd0050, 0x00000000,
	} {
		w.Write32LE(op)
	}

	// return to original address, fixing sp.
	w.Write32LE(uint32(0xdfbf0000)) // ld ra $0000(sp)
	w.Write32LE(uint32(0x03e00008)) // jr ra
	w.Write32LE(uint32(0x27bd0010)) // addiu sp, sp $0010

	// padding
	for w.BodyLen() < targetBodySize-8 {
		w.Write8(uint8(0))
	}

	// Reproduce client stack.
	w.Write16LE(0)
	w.Write16LE(lobbyID)

	// Overwrite return addr in stack for client to run the function.
	w.Write32LE(uint32(0x00c22cc0 + 0x68))

	return a
}
