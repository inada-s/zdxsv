package main

import (
	"fmt"
	"testing"
)

func readU16s(m *LbsMessage) []uint16 {
	r := m.Reader()
	var got []uint16
	for r.Remaining() >= 2 {
		got = append(got, r.Read16())
	}
	if r.Remaining() != 0 {
		got = append(got, 0xdead)
	}
	return got
}

// The game reads lobby and room user counts (0x6303, 0x6403) as id and two
// 16-bit values, answer and notice alike; a value left out is read from
// stale buffer bytes.
func TestLobbyAndRoomUserCountFields(t *testing.T) {
	UseTestDB(t)
	addr, app := startLobbyApp(t, NewMcs())

	const lobbyID, roomID = 4, 1
	var cs []*testClient
	for i := 0; i < 2; i++ {
		c := dialLobby(t, addr)
		id, _, _ := c.login(registerAccount(t), "******", fmt.Sprintf("pilot%d", i))
		c.ask(0x6305, u16(lobbyID))
		cs = append(cs, c)
		if i == 0 {
			app.Locked(func(a *Lbs) {
				a.battles["s"] = NewBattle(a.users[id].Platform, lobbyID)
			})
		}
	}
	watcher, owner := cs[0], cs[1]

	if got := fmt.Sprint(readU16s(watcher.ask(0x6303, u16(lobbyID)))); got != "[4 2 1]" {
		t.Errorf("0x6303 answer %s, want [4 2 1] (lobby, users, battles)", got)
	}

	if a := owner.ask(0x6407, u16(roomID)); a.Status != StatusSuccess {
		t.Fatalf("RequestCreateRoom status %x", a.Status)
	}
	q := NewClientQuestion(0x6609)
	q.Writer().Write(encryptString(q.Seq, "room"))
	owner.send(q)
	owner.expect(0x6609, CategoryAnswer)
	owner.ask(0x660C, nil)

	if got := fmt.Sprint(readU16s(watcher.expect(0x6403, CategoryNotice))); got != "[1 1 0]" {
		t.Errorf("0x6403 notice %s, want [1 1 0]", got)
	}
}
