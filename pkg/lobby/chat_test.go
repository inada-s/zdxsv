package lobby

import (
	"fmt"
	"testing"

	"golang.org/x/text/encoding/japanese"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

// say sends a chat line. The lobby does not answer it.
func (c *testClient) say(line string) {
	c.t.Helper()
	n := NewClientNotice(0x6701)
	n.Writer().Write(encryptString(n.Seq, line))
	c.send(n)
}

// heard returns "<user ID>:<line>" of each chat notice the lobby sent the
// client before it answers an echo sent now.
func (c *testClient) heard() []string {
	c.t.Helper()
	c.ask(0x600E, nil)
	var got []string
	rest := c.queue[:0]
	for _, m := range c.queue {
		if m.Command != 0x6702 || m.Category != CategoryNotice {
			rest = append(rest, m)
			continue
		}
		r := m.Reader()
		userID := r.ReadString()
		r.ReadString() // name
		got = append(got, userID+":"+r.ReadString())
	}
	c.queue = rest
	return got
}

func TestLobbyChatScope(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())

	const lobbyID, roomID = 4, 1
	var cs []*testClient
	var ids []string
	for i := 0; i < 3; i++ {
		c := dialLobby(t, addr)
		id, _, _ := c.login(registerAccount(t), "******", fmt.Sprintf("pilot%d", i))
		c.ask(0x6305, u16(lobbyID))
		cs = append(cs, c)
		ids = append(ids, id)
	}
	a, b, owner := cs[0], cs[1], cs[2]

	// say sends line from cs[from]; want[i] lists what cs[i] hears.
	// The sender is read first: its echo answer means the lobby has
	// handled the line before any other client sends its echo.
	say := func(step string, from int, line string, want ...[]string) {
		t.Helper()
		cs[from].say(line)
		order := []int{from}
		for i := range cs {
			if i != from {
				order = append(order, i)
			}
		}
		for _, i := range order {
			if got := cs[i].heard(); fmt.Sprint(got) != fmt.Sprint(want[i]) {
				t.Errorf("%s: pilot%d heard %v, want %v", step, i, got, want[i])
			}
		}
	}
	line := func(from int, s string) []string { return []string{ids[from] + ":" + s} }

	if a := owner.ask(0x6407, u16(roomID)); a.Status != StatusSuccess {
		t.Fatalf("RequestCreateRoom status %x", a.Status)
	}
	q := NewClientQuestion(0x6609)
	q.Writer().Write(encryptString(q.Seq, "chatroom"))
	owner.send(q)
	owner.expect(0x6609, CategoryAnswer)
	owner.ask(0x660C, nil)

	say("lobby, owner in room", 0, "l1", line(0, "l1"), line(0, "l1"), nil)
	say("room alone", 2, "r1", nil, nil, line(2, "r1"))

	b.ask(0x6406, append(u16(roomID), 0, 0, 0, 0))
	owner.expect(0x6503, CategoryNotice)
	say("room of two, owner", 2, "r2", nil, line(2, "r2"), line(2, "r2"))
	say("room of two, guest", 1, "r3", nil, line(1, "r3"), line(1, "r3"))

	b.ask(0x6501, nil)
	if left := owner.expect(0x6502, CategoryNotice).Reader().ReadString(); left != ids[1] {
		t.Errorf("owner saw %q leave", left)
	}
	say("guest back in lobby", 0, "l2", line(0, "l2"), line(0, "l2"), nil)

	b.ask(0x6406, append(u16(roomID), 0, 0, 0, 0))
	owner.expect(0x6503, CategoryNotice)
	owner.ask(0x6501, nil)
	b.expect(0x6505, CategoryNotice)
	if s := roomStatus(a, roomID); s != model.RoomStateEmpty {
		t.Errorf("room status after the owner left %d", s)
	}
	say("room removed", 1, "l3", line(1, "l3"), line(1, "l3"), line(1, "l3"))
}

// Lobby 1 starts a one player test battle on entry, which needs a UDP proxy
// registered within the last 20 seconds.
func TestLobbyTestBattleWithoutProxy(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())

	c := dialLobby(t, addr)
	c.login(registerAccount(t), "******", "pilot")
	c.ask(0x6305, u16(1))
	c.ask(0x640E, []byte{model.EntryAeug})

	want, err := japanese.ShiftJIS.NewEncoder().String("SERVER:UDPプロキシが登録されていません")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.heard(); fmt.Sprint(got) != fmt.Sprint([]string{want}) {
		t.Errorf("heard %q, want %q", got, want)
	}
	for _, m := range c.queue {
		if m.Command == 0x6910 {
			t.Errorf("battle start notice without a proxy")
		}
	}
}
