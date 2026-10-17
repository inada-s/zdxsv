package lobby

import (
	"fmt"
	"net"
	"testing"

	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

// addPeer adds a logged in peer to app; sent messages stay in its Conn's outbuf.
func addPeer(t *testing.T, app *App, userID string) *AppPeer {
	t.Helper()
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	c, err := net.DialTCP("tcp", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	p := &AppPeer{app: app, conn: NewConn(c), Platform: model.PlatformConsole}
	p.User = model.User{User: db.User{UserID: userID, Name: userID, SessionID: "S" + userID}}
	app.users[userID] = p
	return p
}

// chatLines returns the chat lines in p's unsent output and clears it.
func chatLines(p *AppPeer) []string {
	var got []string
	buf := p.conn.outbuf
	for len(buf) > 0 {
		n, m := message.Deserialize(buf)
		if n == 0 {
			break
		}
		buf = buf[n:]
		if m.Command == 0x6702 {
			r := m.Reader()
			from := r.ReadString()
			r.ReadString()
			got = append(got, from+":"+r.ReadString())
		}
	}
	p.conn.outbuf = p.conn.outbuf[:0]
	return got
}

// Chat in the battle after room reaches only the battle's users in that
// room, not a battle user still elsewhere.
func TestChatInBattleAfterRoom(t *testing.T) {
	app := NewApp()
	a, b, c := addPeer(t, app, "A"), addPeer(t, app, "B"), addPeer(t, app, "C")
	battle := model.NewBattle(model.PlatformConsole, 0)
	for _, p := range []*AppPeer{a, b, c} {
		battle.Add(&p.User)
		app.battles[p.SessionID] = battle
	}
	app.OnEnterBattleAfterRoom(a)
	app.OnEnterBattleAfterRoom(b)
	c.Battle = battle
	for _, p := range []*AppPeer{a, b, c} {
		chatLines(p)
	}

	app.OnSendChatMessage(a, "gg")
	for _, w := range []struct {
		p    *AppPeer
		want string
	}{{a, "[A:gg]"}, {b, "[A:gg]"}, {c, "[]"}} {
		if got := chatLines(w.p); fmt.Sprint(got) != w.want {
			t.Errorf("%s heard %v, want %s", w.p.UserID, got, w.want)
		}
	}
}
