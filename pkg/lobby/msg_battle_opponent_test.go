package lobby

import (
	"net"
	"testing"

	"zdxsv/pkg/db"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

// newCapturePeer returns a peer whose sent messages stay in its Conn's outbuf.
func newCapturePeer(t *testing.T) *AppPeer {
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
	return &AppPeer{app: &App{}, conn: NewConn(c)}
}

func sentMessage(t *testing.T, p *AppPeer) *Message {
	t.Helper()
	n, m := Deserialize(p.conn.outbuf)
	if m == nil || n != len(p.conn.outbuf) {
		t.Fatalf("want one sent message, outbuf % x", p.conn.outbuf)
	}
	return m
}

func TestGetBattleOpponentStatus(t *testing.T) {
	b := model.NewBattle(model.PlatformConsole, 2)
	b.Add(&model.User{User: db.User{UserID: "A", BattleCount: 10, WinCount: 1500, LoseCount: 3}, Entry: model.EntryAeug})
	b.Add(&model.User{User: db.User{UserID: "B"}, Entry: model.EntryTitans})
	handler := defaultHandlers.handlers[0x6917]

	cases := []struct {
		name   string
		battle *model.Battle
		pos    byte
		status uint32
	}{
		{"pos 1", b, 1, StatusSuccess},
		{"pos past the users", b, 3, StatusError},
		{"pos 0", b, 0, StatusError},
		{"no battle", nil, 1, StatusError},
	}
	for _, c := range cases {
		p := newCapturePeer(t)
		p.Battle = c.battle
		req := NewClientQuestion(0x6917)
		req.Writer().Write8(c.pos)
		handler(p, req)
		a := sentMessage(t, p)
		if a.Status != c.status || a.Seq != req.Seq {
			t.Errorf("%s: status %x seq %d, want %x seq %d", c.name, a.Status, a.Seq, c.status, req.Seq)
		}
		if c.status != StatusSuccess {
			continue
		}
		r := a.Reader()
		if pos, class := r.Read8(), r.Read16(); pos != 1 || class != 14 {
			t.Errorf("pos %d class %d, want 1 14", pos, class)
		}
		r.Read32()
		if bc, wc, lc := r.Read32(), r.Read32(), r.Read32(); bc != 10 || wc != 1500 || lc != 3 {
			t.Errorf("counts %d %d %d", bc, wc, lc)
		}
	}
}
