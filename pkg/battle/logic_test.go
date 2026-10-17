package battle

import (
	"sync"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/proto"
)

type fakePeer struct {
	BasePeer
	mu     sync.Mutex
	msgs   []*proto.BattleMessage
	data   [][]byte
	addr   string
	closed bool
}

func (p *fakePeer) AddSendData(b []byte) { p.mu.Lock(); p.data = append(p.data, b); p.mu.Unlock() }
func (p *fakePeer) AddSendMessage(m *proto.BattleMessage) {
	p.mu.Lock()
	p.msgs = append(p.msgs, m)
	p.mu.Unlock()
}
func (p *fakePeer) Address() string { return p.addr }
func (p *fakePeer) Close() error    { p.closed = true; return nil }

func TestParseSessionID(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{"0000100001", "00000000", false},
		{"1234667890", "23457889", false},
		{"123456789", "", true},
		{"12345678901", "", true},
		{"12345abcde", "", true},
	}
	for _, c := range cases {
		got, err := ParseSessionID(c.in)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("ParseSessionID(%q) = %q, %v; want %q, err %v", c.in, got, err, c.want, c.err)
		}
	}
}

func TestIsFinData(t *testing.T) {
	if !IsFinData([]byte{4, 240, 0, 0}) {
		t.Error("fin data not detected")
	}
	for _, b := range [][]byte{nil, {4, 240, 0}, {4, 240, 0, 1}, {4, 240, 0, 0, 0}, {5, 240, 0, 0}} {
		if IsFinData(b) {
			t.Errorf("IsFinData(% x) = true", b)
		}
	}
}

func TestBasePeer(t *testing.T) {
	var p BasePeer
	p.SetUserID("U1")
	p.SetSessionID("S1")
	p.SetPosition(3)
	p.SetRoomID(7)
	if p.UserID() != "U1" || p.SessionID() != "S1" || p.Position() != 3 || p.RoomID() != 7 {
		t.Errorf("BasePeer = %+v", p)
	}
}

func TestRoomRelay(t *testing.T) {
	r := newRoom(5)
	peers := []*fakePeer{{addr: "a"}, {addr: "b"}, {addr: "c"}}
	for i, p := range peers {
		r.Join(p)
		if p.Position() != i || p.RoomID() != 5 {
			t.Fatalf("peer %d: pos %d room %d", i, p.Position(), p.RoomID())
		}
	}
	msg := &proto.BattleMessage{Body: []byte{1}}
	r.SendMessage(peers[1], msg)
	if len(peers[0].msgs) != 1 || len(peers[1].msgs) != 0 || len(peers[2].msgs) != 1 {
		t.Errorf("relay counts %d %d %d, want 1 0 1", len(peers[0].msgs), len(peers[1].msgs), len(peers[2].msgs))
	}

	r.Leave(peers[0])
	r.SendMessage(peers[1], msg)
	if len(peers[0].msgs) != 1 || len(peers[2].msgs) != 2 {
		t.Errorf("left peer still receives")
	}
	if len(r.peers) != 3 {
		t.Errorf("Leave must keep positions: %d peers", len(r.peers))
	}
	r.Leave(peers[1])
	r.Leave(peers[2])
	if len(r.peers) != 0 {
		t.Errorf("room not cleared when empty: %d peers", len(r.peers))
	}
	p := &fakePeer{}
	r.Join(p)
	if p.Position() != 0 {
		t.Errorf("join after clear: pos %d", p.Position())
	}
}

func TestLogicJoin(t *testing.T) {
	l := NewLogic()
	var reply int
	for _, g := range [][]battlerpc.User{
		{{UserID: "U1", SessionID: "S1"}, {UserID: "U2", SessionID: "S2"}},
		{{UserID: "U3", SessionID: "S3"}},
	} {
		l.NotifyBattleUsers(&battlerpc.BattleInfoArgs{Users: g}, &reply)
	}

	if room := l.Join(&fakePeer{}, "unknown"); room != nil {
		t.Errorf("unknown session joined room %d", room.id)
	}
	p1, p2, p3 := &fakePeer{}, &fakePeer{}, &fakePeer{}
	r1 := l.Join(p1, "S1")
	r2 := l.Join(p2, "S2")
	r3 := l.Join(p3, "S3")
	if r1 == nil || r1 != r2 || r3 == nil || r3 == r1 {
		t.Fatalf("rooms %p %p %p", r1, r2, r3)
	}
	if p1.UserID() != "U1" || p1.SessionID() != "S1" || p2.Position() != 1 || p3.RoomID() != 1 {
		t.Errorf("peer state: %+v %+v %+v", p1.BasePeer, p2.BasePeer, p3.BasePeer)
	}
	if room := l.Join(&fakePeer{}, "S1"); room != nil {
		t.Errorf("session joined twice")
	}

	// a battle info that does not list the session itself
	l.BattleRpc.NotifyBattleUsers(&battlerpc.BattleInfoArgs{Users: []battlerpc.User{{SessionID: "S4"}}}, &reply)
	info := l.GetBattleInfo("S4")
	info.Users[0].SessionID = "other"
	if _, ok := l.FindWaitingUser("S4"); ok {
		t.Errorf("FindWaitingUser found a session missing from its battle info")
	}
}
