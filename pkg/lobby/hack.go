package lobby

import (
	"fmt"
	"zdxsv/pkg/db"
	. "zdxsv/pkg/lobby/message"
)

func SendLobbyChatHackNotice(p *AppPeer) {
	NoticeChatMessage(p, "SERVER", "＞", "自動選抜に入ることでＵＤＰプロキシの")
	NoticeChatMessage(p, "SERVER", "＞", "接続テスト対戦を行うことができます。")
	NoticeChatMessage(p, "SERVER", "＞", "利用可能コマンド")
	NoticeChatMessage(p, "SERVER", "＞", "ｗ０：ワイドスクリーンパッチ無効化")
	NoticeChatMessage(p, "SERVER", "＞", "ｗ１：ワイドスクリーンパッチ有効化")
}

func LobbyChatHack(p *AppPeer, str string) {
	switch str {
	case "ｗ０":
		p.System = (p.System & uint32(0xffffff00)) | 0x00
		NoticeChatMessage(p, "SERVER", "＞", "ワイドスクリーンパッチを無効化")
		NoticeChatMessage(p, "SERVER", "＞", "戦場選択画面に戻ってください")
		NoticeChatMessage(p, "SERVER", "＞", "この設定はユーザ別に保存されます")
		db.DefaultDB.UpdateUser(&p.User.User)
	case "ｗ１":
		p.System = (p.System & uint32(0xffffff00)) | 0x01
		NoticeChatMessage(p, "SERVER", "＞", "ワイドスクリーンパッチを有効化")
		NoticeChatMessage(p, "SERVER", "＞", "戦場選択画面に戻ってください")
		NoticeChatMessage(p, "SERVER", "＞", "この設定はユーザ別に保存されます")
		db.DefaultDB.UpdateUser(&p.User.User)
	}
}

// SetPadDelayLobbyHack writes answer of LobbyExplain message
// with a function that sets pad delay to static value.
func SetPadDelayLobbyHack(p *AppPeer, m *Message) *Message {
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
		0x24040002, 0x24050004, 0x3c030060, 0x2463fba0, // a0 = 2, a1 = 4, v1 = 0x005ffba0(table)

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
func SetWideScreenLobbyHack(p *AppPeer, m *Message) *Message {
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
