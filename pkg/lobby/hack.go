package lobby

import (
	"fmt"

	. "zdxsv/pkg/lobby/message"

	"github.com/golang/glog"
)

// SetPadDelayLobbyHack writes answer of LobbyExplain message
// with a function that sets pad delay to static value.
// NOTE: This function only works when lobby_id = 3.
func SetPadDelayLobbyHack(p *AppPeer, m *Message) *Message {
	lobbyId := m.Reader().Read16()
	a := NewServerAnswer(m)
	w := a.Writer()

	if lobbyId != uint16(3) {
		glog.Warningln("SetPadDelay Failed lobbyId must be 3")
		w.Write16(lobbyId)
		w.WriteString(fmt.Sprintf("<B>Lobby %d<B>", lobbyId))
		return a
	}

	// FIX DELAY TABLE HACK

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

	// R5900 Function: Fill pad delay table
	// (initial, soft_limit, hard_limit) * 6 to static value.
	fixLagTable := []uint32{
		0x27bdffb0, // sp -= 0x0050

		0xffa40040, 0xffa50030, 0xffa20020, 0xffa30010, // save a0, a1, v0, v1 to stack
		0x24040002, 0x24050006, 0x3c030060, 0x2463fba0, // a0 = 2, a1 = 6, v1 = 0x005ffba0(table)

		0xa0640000, 0xa0650004, 0xa0650008, // table[0] = (a0, a1, a1)
		0xa064000c, 0xa0650010, 0xa0650014, // table[1] = (a0, a1, a1)
		0xa0640018, 0xa065001c, 0xa0650020, // table[2] = (a0, a1, a1)
		0xa0640024, 0xa0650028, 0xa065002c, // table[3] = (a0, a1, a1)
		0xa0640030, 0xa0650034, 0xa0650038, // table[4] = (a0, a1, a1)
		0xa064003c, 0xa0650040, 0xa0650044, // table[5] = (a0, a1, a1)

		0xdfa40040, 0xdfa50030, 0xdfa20020, 0xdfa30010, // load a0, a1, v0, v1 from stack

		0x27bd0050, // sp += 0x0050
	}

	for _, op := range fixLagTable {
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
	w.Write16LE(lobbyId)

	// Overwrite return addr in stack for client to run the function.
	w.Write32LE(uint32(0x00c22cc0))

	return a
}
