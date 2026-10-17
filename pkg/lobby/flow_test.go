package lobby

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"strconv"
	"testing"
	"time"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/config"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

func listenLocal(t *testing.T) *net.TCPListener {
	t.Helper()
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// startLobby runs a lobby App and its TCP server against the battle RPC b.
// It returns the lobby address.
func startLobby(t *testing.T, b *battlerpc.BattleRpc) string {
	t.Helper()
	addr, _ := startLobbyApp(t, b)
	return addr
}

// startLobbyApp is startLobby that also returns the App.
func startLobbyApp(t *testing.T, b *battlerpc.BattleRpc) (string, *App) {
	t.Helper()
	return startLobbyAppWith(t, b, func(*App) {})
}

// startLobbyAppWith is startLobbyApp with setup run on the App before it serves.
func startLobbyAppWith(t *testing.T, b *battlerpc.BattleRpc, setup func(*App)) (string, *App) {
	t.Helper()
	rl := listenLocal(t)
	rs := rpc.NewServer()
	if err := rs.RegisterName("Logic", b); err != nil {
		t.Fatal(err)
	}
	go http.Serve(rl, rs)

	config.Conf.Battle.RPCAddr = rl.Addr().String()
	config.Conf.Battle.PublicAddr = "127.0.0.2:8210"
	config.Conf.Lobby.RPCAddr = "127.0.0.1:0"

	app := NewApp()
	setup(app)
	go app.Serve()
	t.Cleanup(app.Quit)

	ll := listenLocal(t)
	go NewServer(app).Serve(ll)
	return ll.Addr().String(), app
}

// encryptString is the client side of MessageBodyReader.ReadEncryptedString.
func encryptString(seq uint16, s string) []byte {
	plain := []byte(s)
	sum := uint16(0)
	enc := make([]byte, len(plain))
	p := byte(seq)
	fixval := [...]byte{21, 23, 10, 17, 23, 19, 6, 13}
	masks := [...]byte{0x33, 0x30, 0x3c, 0x34, 0x2d, 0x30, 0x3c, 0x34}
	for j, x := range plain {
		i := byte(j)
		enc[i] = x ^ (fixval[i&7] - (i & 0xf8) - p + ((p-9+i)&masks[i&7])*2)
		sum += uint16(x)
	}
	buf := make([]byte, 4, 4+len(enc))
	binary.BigEndian.PutUint16(buf, uint16(len(enc)+2))
	binary.BigEndian.PutUint16(buf[2:], sum)
	return append(buf, enc...)
}

// keyPairKey1 is the key1 string from which the 0x6101 handler derives sessionID.
func keyPairKey1(sessionID string) string {
	v, _ := strconv.ParseUint("0"+sessionID[:4]+"0"+sessionID[4:], 10, 64)
	return fmt.Sprint(v + 100001)
}

type testClient struct {
	t     *testing.T
	conn  net.Conn
	buf   []byte
	queue []*Message
}

func dialLobby(t *testing.T, addr string) *testClient {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &testClient{t: t, conn: c}
}

func (c *testClient) send(m *Message) {
	c.t.Helper()
	if _, err := c.conn.Write(m.Serialize()); err != nil {
		c.t.Fatal(err)
	}
}

// expect returns the first server message with the command not yet expected.
// Other messages stay queued for a later expect.
func (c *testClient) expect(command uint16, category byte) *Message {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	tmp := make([]byte, 4096)
	for {
		for {
			n, m := Deserialize(c.buf)
			if m == nil {
				break
			}
			c.buf = c.buf[n:]
			c.queue = append(c.queue, m)
		}
		for i, m := range c.queue {
			if m.Command == command && m.Category == category {
				c.queue = append(c.queue[:i], c.queue[i+1:]...)
				return m
			}
		}
		n, err := c.conn.Read(tmp)
		if err != nil {
			c.t.Fatalf("waiting for 0x%04x: %v", command, err)
		}
		c.buf = append(c.buf, tmp[:n]...)
	}
}

// ask sends a client question and returns the server answer.
func (c *testClient) ask(command uint16, body []byte) *Message {
	c.t.Helper()
	q := NewClientQuestion(command)
	q.Writer().Write(body)
	c.send(q)
	a := c.expect(command, CategoryAnswer)
	if a.Seq != q.Seq {
		c.t.Fatalf("0x%04x answer seq %d, question %d", command, a.Seq, q.Seq)
	}
	return a
}

// login logs in as userID ("******" registers a new user) and returns the
// decided user ID, the user IDs the lobby offered and its battle result question.
func (c *testClient) login(ac *db.Account, userID, name string) (string, []string, *Message) {
	c.t.Helper()
	kp := c.expect(0x6101, CategoryQuestion)
	a := NewClientAnswer(kp)
	a.Writer().WriteString(keyPairKey1(ac.SessionID)).Write(encryptString(a.Seq, ac.LoginKey))
	c.send(a)

	c.send(NewClientAnswer(c.expect(0x6103, CategoryQuestion)))
	r := c.expect(0x6131, CategoryNotice).Reader()
	var listed []string
	for n := r.Read8(); n > 0; n-- {
		listed = append(listed, r.ReadString())
		r.ReadString()
		r.ReadString()
	}

	q := NewClientQuestion(0x6132)
	q.Writer().Write(append(encryptString(q.Seq, userID), encryptString(q.Seq, name)...))
	c.send(q)
	ask := c.expect(0x6138, CategoryQuestion)
	c.expect(0x6104, CategoryNotice)
	decided := c.expect(0x6132, CategoryAnswer).Reader().ReadString()
	if len(decided) != 6 {
		c.t.Fatalf("user id %q", decided)
	}
	return decided, listed, ask
}

// battleResult is the client's answer body to the 0x6138 battle result question.
func battleResult(seq uint16, battleCode string, rounds, win, lose byte, kill, death, frame uint32, side byte) []byte {
	m := &Message{}
	w := m.Writer()
	w.Write16(0)
	w.Write(encryptString(seq, battleCode))
	w.Write([]byte{0, 0, 0, 0, rounds, win, lose})
	w.Write32(kill).Write32(death).Write32(frame).Write32(0).Write32(0)
	w.Write([]byte{side, 0, 0, 0, 0})
	w.Write(make([]byte, 16))
	return m.Body
}

func registerAccount(t *testing.T) *db.Account {
	t.Helper()
	ac, err := db.DefaultDB.RegisterAccount("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	// The login server issues the session ID the lobby checks.
	if err := db.DefaultDB.LoginAccount(ac); err != nil {
		t.Fatal(err)
	}
	return ac
}

func u16(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }

// Clients log in, enter a lobby and entry: the lobby starts the battle, tells
// the battle server and records it; after the battle they report the result.
func TestLobbyBattleRoundTrip(t *testing.T) {
	a, ti := byte(model.EntryAeug), byte(model.EntryTitans)
	cases := []struct {
		name    string
		lobbyID uint16
		sides   []byte
		counted bool // the result adds to the user's totals (4 player battles only)
	}{
		{"1vs1 lobby", 2, []byte{a, ti}, false},
		{"2vs2 lobby", 4, []byte{a, ti, a, ti}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			battleRoundTrip(t, c.lobbyID, c.sides, c.counted)
		})
	}
}

func battleRoundTrip(t *testing.T, lobbyID uint16, sides []byte, counted bool) {
	dbtest.Use(t)
	brpc := battlerpc.NewBattleRpc()
	addr := startLobby(t, brpc)

	type player struct {
		c      *testClient
		ac     *db.Account
		userID string
		side   byte
		pos    byte
	}
	var players []*player
	for _, side := range sides {
		players = append(players, &player{side: side})
	}
	for i, p := range players {
		p.ac = registerAccount(t)
		p.c = dialLobby(t, addr)
		var listed []string
		p.userID, listed, _ = p.c.login(p.ac, "******", fmt.Sprintf("pilot%d", i))
		if len(listed) != 0 {
			t.Fatalf("new account lists %v", listed)
		}
	}

	for _, p := range players {
		if a := p.c.ask(0x6305, u16(lobbyID)); a.Status != StatusSuccess {
			t.Fatalf("EnterLobby status %x", a.Status)
		}
	}
	for _, p := range players {
		p.c.ask(0x640E, []byte{p.side})
	}
	for _, p := range players {
		p.c.expect(0x6910, CategoryNotice)
	}

	var battleCode string
	for i, p := range players {
		if n := p.c.ask(0x6911, nil).Reader().Read8(); int(n) != len(players) {
			t.Errorf("user count %d", n)
		}
		if p.pos = p.c.ask(0x6912, nil).Reader().Read8(); p.pos == 0 || int(p.pos) > len(players) {
			t.Errorf("player %d position %d", i, p.pos)
		}
		code := p.c.ask(0x6915, nil).Reader().ReadString()
		if i == 0 {
			battleCode = code
		} else if code != battleCode {
			t.Errorf("battle codes %q %q", battleCode, code)
		}
		sv := p.c.ask(0x6916, nil).Body
		if want := []byte{0, 4, 127, 0, 0, 2, 0, 2, 0x20, 0x12}; !bytes.Equal(sv, want) {
			t.Errorf("battle server address % x want % x", sv, want)
		}
	}

	info := brpc.GetBattleInfo(players[0].ac.SessionID)
	if info == nil || len(info.Users) != len(players) {
		t.Fatalf("battle server info %+v", info)
	}
	for i, p := range players {
		if brpc.GetBattleInfo(p.ac.SessionID) != info {
			t.Errorf("player %d battle server info %+v", i, brpc.GetBattleInfo(p.ac.SessionID))
		}
		u := info.Users[p.pos-1]
		if u.UserID != p.userID || u.Entry != p.side || u.Name != fmt.Sprintf("pilot%d", i) {
			t.Errorf("battle server user %d: %+v", i, u)
		}
		rec, err := db.DefaultDB.GetBattleRecordUser(battleCode, p.userID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Pos != int(p.pos) || rec.Side != int(p.side) || rec.Players != len(players) {
			t.Errorf("battle record %+v", rec)
		}
	}

	// Each client leaves for the battle server, then reconnects with the
	// same session: the lobby offers only the battle user, takes the result
	// and both meet in the battle after room.
	for _, p := range players {
		p.c.ask(0x6002, nil)
		p.c.conn.Close()
	}
	for i, p := range players {
		p.c = dialLobby(t, addr)
		userID, listed, ask := p.c.login(p.ac, p.userID, fmt.Sprintf("pilot%d", i))
		if userID != p.userID || len(listed) != 1 || listed[0] != p.userID {
			t.Fatalf("relogin as %q listed %v", userID, listed)
		}
		a := NewClientAnswer(ask)
		win, lose := byte(3-i), byte(2+i)
		a.Writer().Write(battleResult(a.Seq, battleCode, 5, win, lose, 7, 4, 12345, p.side))
		p.c.send(a)
		if a := p.c.ask(0x6210, nil); a.Status != StatusSuccess {
			t.Fatalf("EnterBattleAfterRoom status %x", a.Status)
		}
	}
	if n := players[1].c.ask(0x6212, nil).Reader().Read16(); int(n) != len(players) {
		t.Errorf("battle after room user count %d", n)
	}

	for i, p := range players {
		win, lose := 3-i, 2+i
		rec, err := db.DefaultDB.GetBattleRecordUser(battleCode, p.userID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Round != 5 || rec.Win != win || rec.Lose != lose || rec.Kill != 7 || rec.Death != 4 || rec.Frame != 12345 {
			t.Errorf("battle record after result %+v", rec)
		}
		u, err := db.DefaultDB.GetUser(p.userID)
		if err != nil {
			t.Fatal(err)
		}
		kill := 7
		if !counted {
			win, lose, kill = 0, 0, 0
		}
		if u.WinCount != win || u.LoseCount != lose || u.KillCount != kill {
			t.Errorf("user %s after result: win %d lose %d kill %d", p.userID, u.WinCount, u.LoseCount, u.KillCount)
		}
	}
}

func roomStatus(c *testClient, roomID uint16) byte {
	r := c.ask(0x6404, u16(roomID)).Reader()
	r.Read16()
	return r.Read8()
}

// One client creates a room with a rule, another joins, both entry and the
// owner starts: the battle carries the room's rule.
func TestLobbyRoomBattle(t *testing.T) {
	dbtest.Use(t)
	brpc := battlerpc.NewBattleRpc()
	addr := startLobby(t, brpc)

	const lobbyID, roomID = 4, 1
	var cs []*testClient
	var ids []string
	for i := 0; i < 2; i++ {
		c := dialLobby(t, addr)
		id, _, _ := c.login(registerAccount(t), "******", fmt.Sprintf("pilot%d", i))
		c.ask(0x6305, u16(lobbyID))
		cs = append(cs, c)
		ids = append(ids, id)
	}
	owner, guest := cs[0], cs[1]

	if s := roomStatus(guest, roomID); s != model.RoomStateEmpty {
		t.Fatalf("room status before create %d", s)
	}
	if a := owner.ask(0x6407, u16(roomID)); a.Status != StatusSuccess {
		t.Fatalf("RequestCreateRoom status %x", a.Status)
	}
	if a := guest.ask(0x6407, u16(roomID)); a.Status != StatusError {
		t.Errorf("second RequestCreateRoom status %x", a.Status)
	}
	q := NewClientQuestion(0x6609)
	q.Writer().Write(encryptString(q.Seq, "testroom"))
	owner.send(q)
	owner.expect(0x6609, CategoryAnswer)
	const battleTime, battleTimeElem = 3, 1
	owner.ask(0x660B, []byte{battleTime, battleTimeElem})
	owner.ask(0x660C, nil)

	if s := roomStatus(guest, roomID); s != model.RoomStateRecruit {
		t.Fatalf("room status after create %d", s)
	}
	if name := guest.ask(0x6402, u16(roomID)).Reader(); name.Read16() != roomID || name.ReadString() != "testroom" {
		t.Errorf("room name")
	}
	guest.ask(0x6406, append(u16(roomID), 0, 0, 0, 0))
	if joined := owner.expect(0x6503, CategoryNotice).Reader().ReadString(); joined != ids[1] {
		t.Errorf("owner saw %q join", joined)
	}

	owner.ask(0x6504, []byte{model.EntryAeug})
	guest.ask(0x6504, []byte{model.EntryTitans})
	r := guest.ask(0x6412, u16(roomID)).Reader()
	if r.Read16() != roomID || r.Read16() != 1 || r.Read16() != 1 {
		t.Errorf("room entry user count")
	}
	owner.send(NewClientNotice(0x6508))

	want := model.NewRule()
	want.Set(battleTime, battleTimeElem)
	if bytes.Equal(want.Serialize(), model.NewRule().Serialize()) {
		t.Fatal("the rule change does not show in the serialized rule")
	}
	for i, c := range cs {
		c.expect(0x6910, CategoryNotice)
		if rule := c.ask(0x6914, nil).Body; !bytes.Equal(rule, want.Serialize()) {
			t.Errorf("player %d battle rule % x\nwant % x", i, rule, want.Serialize())
		}
		if n := c.ask(0x6911, nil).Reader().Read8(); n != 2 {
			t.Errorf("player %d user count %d", i, n)
		}
	}
}
