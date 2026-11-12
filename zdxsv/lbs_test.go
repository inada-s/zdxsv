package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
)

// pipeConn is one end of an in-memory connection that reports addr as its peer's address.
type pipeConn struct {
	net.Conn
	addr net.Addr
}

func (c pipeConn) RemoteAddr() net.Addr { return c.addr }

var pipePort atomic.Int32

// newPipe returns the server and the client end of an in-memory connection.
// Each server end has its own loopback address, as Lbs tells clients apart by it.
func newPipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	s, c := net.Pipe()
	t.Cleanup(func() { c.Close(); s.Close() })
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 20000 + int(pipePort.Add(1))}
	return pipeConn{s, addr}, c
}

// testLobby is a lobby that clients reach through in-memory connections.
type testLobby struct {
	sv *LbsServer
}

// startLobby runs a Lbs with the Mcs b in process.
func startLobby(t *testing.T, b *Mcs) *testLobby {
	t.Helper()
	l, _ := startLobbyApp(t, b)
	return l
}

// startLobbyApp is startLobby that also returns the Lbs.
func startLobbyApp(t *testing.T, b *Mcs) (*testLobby, *Lbs) {
	t.Helper()
	return startLobbyAppWith(t, b, func(*Lbs) {})
}

// startLobbyAppWith is startLobbyApp with setup run on the Lbs before it serves.
func startLobbyAppWith(t *testing.T, b *Mcs, setup func(*Lbs)) (*testLobby, *Lbs) {
	t.Helper()
	Conf.Battle.PublicAddr = "127.0.0.2:8210"

	app := NewLbs()
	app.SetBattleServer(b)
	setup(app)
	go app.Serve()
	t.Cleanup(app.Quit)
	return &testLobby{sv: NewLbsServer(app)}, app
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
	queue []*LbsMessage
}

func dialLobby(t *testing.T, l *testLobby) *testClient {
	t.Helper()
	s, c := newPipe(t)
	go l.sv.ServeConn(s)
	return &testClient{t: t, conn: c}
}

func (c *testClient) send(m *LbsMessage) {
	c.t.Helper()
	if _, err := c.conn.Write(m.Serialize()); err != nil {
		c.t.Fatal(err)
	}
}

// expect returns the first server message with the command not yet expected.
// Other messages stay queued for a later expect.
func (c *testClient) expect(command uint16, category byte) *LbsMessage {
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

func (c *testClient) sendCustom(command uint16, body []byte) {
	c.t.Helper()
	m := NewClientNotice(command)
	m.Category = CategoryCustom
	m.Body = body
	c.send(m)
}

// platformInfo is the 0x9950 body the emulator of player i sends on connect.
func platformInfo(i int) []byte {
	return []byte(fmt.Sprintf("emulator=pcsx2\nsync=0123456789abcdef\nudp=1\nudp_addr=203.0.113.%d:40000\nggpo=%d\n", i+1, 7000+i))
}

// ask sends a client question and returns the server answer.
func (c *testClient) ask(command uint16, body []byte) *LbsMessage {
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
func (c *testClient) login(ac *testLogin, userID, name string) (string, []string, *LbsMessage) {
	c.t.Helper()
	listed := c.chooseUser(ac, userID, name)
	ask := c.expect(0x6138, CategoryQuestion)
	c.expect(0x6104, CategoryNotice)
	decided := c.expect(0x6132, CategoryAnswer).Reader().ReadString()
	if len(decided) != 6 {
		c.t.Fatalf("user id %q", decided)
	}
	return decided, listed, ask
}

// chooseUser runs the login up to the user choice and returns the user IDs the lobby offered.
func (c *testClient) chooseUser(ac *testLogin, userID, name string) []string {
	c.t.Helper()
	kp := c.expect(0x6101, CategoryQuestion)
	a := NewClientAnswer(kp)
	a.Writer().WriteString(keyPairKey1(ac.SessionID)).Write(encryptString(a.Seq, ac.LoginKey))
	c.send(a)
	c.ask(0x61A0, []byte{6})

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
	return listed
}

type personalRecord struct {
	battle, win, lose, invalid, kill uint32
	class                            byte
}

// topPage asks for what the game shows on its top page after login and
// returns the personal record of all battles, as Aeug and as Titans.
func (c *testClient) topPage() [3]personalRecord {
	c.t.Helper()
	c.ask(0x614C, nil)
	c.ask(0x6144, nil)
	var recs [3]personalRecord
	for kind := byte(0); kind < 2; kind++ {
		for page := byte(0); page < 3; page++ {
			r := c.ask(0x6145, []byte{kind, page}).Reader()
			if k, p := r.Read8(), r.Read8(); k != kind || p != page {
				c.t.Fatalf("personal record kind %d page %d, asked %d %d", k, p, kind, page)
			}
			r.Read32()
			r.Read32()
			rec := personalRecord{invalid: r.Read32(), class: r.Read8()}
			r.Read32()
			r.Read32()
			rec.battle, rec.win, rec.lose, rec.kill = r.Read32(), r.Read32(), r.Read32(), r.Read32()
			if kind == 0 {
				recs[page] = rec
			} else if rec != recs[page] {
				c.t.Errorf("personal record page %d: %+v, then %+v", page, recs[page], rec)
			}
		}
	}
	return recs
}

// battleResult is the client's answer body to the 0x6138 battle result question.
func battleResult(seq uint16, battleCode string, rounds, win, lose byte, kill, death, frame uint32, side byte) []byte {
	m := &LbsMessage{}
	w := m.Writer()
	w.Write16(0)
	w.Write(encryptString(seq, battleCode))
	w.Write([]byte{0, 0, 0, 0, rounds, win, lose})
	w.Write32(kill).Write32(death).Write32(frame).Write32(0).Write32(0)
	w.Write([]byte{side, 0, 0, 0, 0})
	w.Write(make([]byte, 16))
	return m.Body
}

// testLogin is what a client got from the login pages: the key it logs in with and its session.
type testLogin struct {
	LoginKey  string
	SessionID string
}

// registerAccount makes an account and logs it in as the login pages do.
func registerAccount(t *testing.T) *testLogin {
	t.Helper()
	ac, err := DefaultDB.RegisterAccount("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return loginAgain(ac.LoginKey)
}

// loginAgain starts another session of the account, as a login from another client does.
func loginAgain(loginKey string) *testLogin {
	id, _ := Sessions.Start(loginKey, false)
	return &testLogin{LoginKey: loginKey, SessionID: id}
}

func u16(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }

// A client walks the plaza and the lobby list the way the game does after login,
// and sees the user who entered a lobby.
func TestLobbyList(t *testing.T) {
	UseTestDB(t)
	addr := startLobby(t, NewMcs())
	in, look := dialLobby(t, addr), dialLobby(t, addr)
	in.login(registerAccount(t), "******", "inside")
	look.login(registerAccount(t), "******", "looker")
	const lobbyID = 4
	if a := in.ask(0x6305, u16(lobbyID)); a.Status != StatusSuccess {
		t.Fatalf("EnterLobby status %x", a.Status)
	}

	plazas := look.ask(0x6203, nil).Reader().Read16()
	if plazas == 0 {
		t.Fatal("no plaza")
	}
	for id := uint16(1); id <= plazas; id++ {
		r := look.ask(0x6205, u16(id)).Reader()
		if got, _, battles := r.Read16(), r.Read16(), r.Read16(); got != id || battles != 0 {
			t.Fatalf("GetPlazaJoinUser(%d) = plaza %d, %d battles", id, got, battles)
		}
		if got := look.ask(0x6206, u16(id)).Reader().Read16(); got != id {
			t.Fatalf("GetPlazaStatus(%d) = plaza %d", id, got)
		}
	}
	if a := look.ask(0x6207, u16(1)); a.Status != StatusSuccess {
		t.Fatalf("EnterPlaza status %x", a.Status)
	}

	lobbies := look.ask(0x6301, nil).Reader().Read16()
	if int(lobbies) >= LobbyCount || lobbies < lobbyID {
		t.Fatalf("GetLobbyCount = %d, lobby ids 1..%d", lobbies, LobbyCount-1)
	}
	for id := uint16(1); id <= lobbies; id++ {
		r := look.ask(0x6303, u16(id)).Reader()
		want := uint16(0)
		if id == lobbyID {
			want = 1
		}
		if got, users := r.Read16(), r.Read16(); got != id || users != want {
			t.Errorf("GetLobbyUserCount(%d) = lobby %d, %d users, want %d", id, got, users, want)
		}
		r = look.ask(0x6304, u16(id)).Reader()
		if got, status := r.Read16(), r.Read8(); got != id || status != 3 {
			t.Errorf("GetLobbyUserStatus(%d) = lobby %d, status %d, want 3 (open)", id, got, status)
		}
		if a := look.ask(0x6308, u16(id)); a.Status != StatusSuccess {
			t.Errorf("GetLobbyExplain(%d) status %x", id, a.Status)
		}
	}
}

// Clients log in, enter a lobby and entry: the lobby starts the battle, tells
// the battle server and records it; after the battle they report the result.
func TestLobbyBattleRoundTrip(t *testing.T) {
	a, ti := byte(EntryAeug), byte(EntryTitans)
	cases := []struct {
		name    string
		lobbyID uint16
		sides   []byte
		counted bool // the result adds to the user's totals (4 player battles only)
		shared  int  // the first players are users of one account, each from its own client
		emu     bool // the clients are emulators running their own UDP bridge
	}{
		{"1vs1 lobby", 2, []byte{a, ti}, false, 0, false},
		{"2vs2 lobby", 4, []byte{a, ti, a, ti}, true, 0, false},
		{"2vs2 of three users of one account", 4, []byte{a, ti, a, ti}, true, 3, false},
		{"2vs2 lobby of emulators", 4, []byte{a, ti, a, ti}, true, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			battleRoundTrip(t, c.lobbyID, c.sides, c.counted, c.shared, c.emu)
		})
	}
}

func battleRoundTrip(t *testing.T, lobbyID uint16, sides []byte, counted bool, shared int, emu bool) {
	conn := UseTestDB(t)
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('console', 2, '1v1', '', '', '')`); err != nil {
		t.Fatal(err)
	}
	brpc := NewMcs()
	addr := startLobby(t, brpc)

	type player struct {
		c      *testClient
		ac     *testLogin
		userID string
		side   byte
		pos    byte
	}
	var players []*player
	for _, side := range sides {
		players = append(players, &player{side: side})
	}
	for i, p := range players {
		want := 0
		if 0 < i && i < shared {
			p.ac, want = loginAgain(players[0].ac.LoginKey), i
		} else {
			p.ac = registerAccount(t)
		}
		p.c = dialLobby(t, addr)
		if emu {
			p.c.sendCustom(0x9950, platformInfo(i))
		}
		var listed []string
		p.userID, listed, _ = p.c.login(p.ac, "******", fmt.Sprintf("pilot%d", i))
		if len(listed) != want {
			t.Fatalf("player %d: account lists %v", i, listed)
		}
		if recs := p.c.topPage(); recs != [3]personalRecord{} {
			t.Errorf("player %d: personal records of a new user %+v", i, recs)
		}
		bin := NewClientQuestion(0x6143)
		bin.Writer().Write(encryptString(bin.Seq, fmt.Sprintf("Pilot%d", i)+strings.Repeat("\x00", 20)))
		p.c.send(bin)
		p.c.expect(0x6143, CategoryAnswer)
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
	}
	for i, p := range players {
		for j, q := range players {
			r := p.c.ask(0x6913, []byte{q.pos}).Reader()
			if pos, entry, userID, name := r.Read8(), r.Read8(), r.ReadString(), r.ReadString(); pos != q.pos || entry != q.side || userID != q.userID || name != fmt.Sprintf("pilot%d", j) {
				t.Errorf("player %d: opponent %d: pos %d entry %d user %q name %q", i, j, pos, entry, userID, name)
			}
			r.ReadString()
			if bin := r.ReadString(); strings.TrimRight(bin, "\x00") != fmt.Sprintf("Pilot%d", j) {
				t.Errorf("player %d: opponent %d binary %q", i, j, bin)
			}
			if kind := r.Read8(); kind != 6 {
				t.Errorf("player %d: opponent %d last byte %d, want 6", i, j, kind)
			}
		}
		for j, q := range players {
			r := p.c.ask(0x6917, []byte{q.pos}).Reader()
			pos, class := r.Read8(), r.Read16()
			r.Read32()
			if battle, win, lose := r.Read32(), r.Read32(), r.Read32(); pos != q.pos || class != 0 || battle != 0 || win != 0 || lose != 0 {
				t.Errorf("player %d: opponent %d status: pos %d class %d counts %d %d %d", i, j, pos, class, battle, win, lose)
			}
		}
	}
	for i, p := range players {
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
	for i, p := range players {
		if !emu {
			break
		}
		m := battleInfo(t, p.c.expect(0x9951, CategoryCustom))
		if len(m.Users) != len(players) {
			t.Fatalf("player %d: battle info users %v", i, m.Users)
		}
		for j, q := range players {
			u, ggpo := m.Users[q.pos-1], 7000+j
			if j == i {
				ggpo = 0
			}
			if u.UserID != q.userID || u.PilotName != fmt.Sprintf("Pilot%d", j) || u.Team != int(q.side) || u.GgpoPort != ggpo {
				t.Errorf("player %d: battle info user %d: %v", i, j, u)
			}
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
		rec, err := DefaultDB.GetBattleRecordUser(battleCode, p.userID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Pos != int(p.pos) || rec.Side != int(p.side) || rec.Players != len(players) {
			t.Errorf("battle record %+v", rec)
		}
	}

	usedMs := [][]int{{3, 5, 7, 9}, {4, 6, 8, 10}}
	// Each client leaves for the battle server, then reconnects with the
	// same session: the lobby offers only the battle user, takes the result
	// and both meet in the battle after room.
	for _, p := range players {
		p.c.ask(0x6002, nil)
		p.c.conn.Close()
	}
	for i, p := range players {
		p.c = dialLobby(t, addr)
		if emu {
			p.c.sendCustom(0x9950, platformInfo(i))
			body := fmt.Sprintf(`{"battle_code":%q,"user_id":%q,"round_data":[{"win_team":1,"used_ms":[1,2,3,4]}]}`, battleCode, p.userID)
			p.c.sendCustom(0x9952, []byte(body))
		}
		userID, listed, ask := p.c.login(p.ac, p.userID, fmt.Sprintf("pilot%d", i))
		if userID != p.userID || len(listed) != 1 || listed[0] != p.userID {
			t.Fatalf("relogin as %q listed %v", userID, listed)
		}
		a := NewClientAnswer(ask)
		win, lose := byte(3-i), byte(2+i)
		a.Writer().Write(battleResult(a.Seq, battleCode, 5, win, lose, 7, 4, 12345, p.side))
		p.c.send(a)
		var want [3]personalRecord
		if counted {
			want[0] = personalRecord{battle: 5, win: uint32(win), lose: uint32(lose), kill: 7}
			want[p.side] = want[0]
		}
		if recs := p.c.topPage(); recs != want {
			t.Errorf("player %d: personal records after the result %+v, want %+v", i, recs, want)
		}
		if a := p.c.ask(0x6210, nil); a.Status != StatusSuccess {
			t.Fatalf("EnterBattleAfterRoom status %x", a.Status)
		}
	}
	if n := players[1].c.ask(0x6212, nil).Reader().Read16(); int(n) != len(players) {
		t.Errorf("battle after room user count %d", n)
	}
	if emu {
		ms, _ := json.Marshal(usedMs)
		h := &APIHandler{ReplayStore: &DirReplayStore{Dir: t.TempDir(), URL: "http://127.0.0.1/lbs/"}}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, replayUploadRequest(t, battleCode+".pb", []byte("replay"), "used_ms", string(ms)))
		if w.Code != 200 {
			t.Fatalf("replay upload code %d body %s", w.Code, w.Body.String())
		}
	}

	for i, p := range players {
		win, lose := 3-i, 2+i
		rec, err := DefaultDB.GetBattleRecordUser(battleCode, p.userID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Round != 5 || rec.Win != win || rec.Lose != lose || rec.Kill != 7 || rec.Death != 4 || rec.Frame != 12345 {
			t.Errorf("battle record after result %+v", rec)
		}
		wantMs := ""
		if emu {
			wantMs = fmt.Sprintf("%d,%d", usedMs[0][p.pos-1], usedMs[1][p.pos-1])
		}
		if rec.UsedMsList != wantMs {
			t.Errorf("player %d used ms %q, want %q", i, rec.UsedMsList, wantMs)
		}
		u, err := DefaultDB.GetUser(p.userID)
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
	UseTestDB(t)
	brpc := NewMcs()
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

	if s := roomStatus(guest, roomID); s != RoomStateEmpty {
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

	if s := roomStatus(guest, roomID); s != RoomStateRecruit {
		t.Fatalf("room status after create %d", s)
	}
	if name := guest.ask(0x6402, u16(roomID)).Reader(); name.Read16() != roomID || name.ReadString() != "testroom" {
		t.Errorf("room name")
	}
	guest.ask(0x6406, append(u16(roomID), 0, 0, 0, 0))
	if joined := owner.expect(0x6503, CategoryNotice).Reader().ReadString(); joined != ids[1] {
		t.Errorf("owner saw %q join", joined)
	}

	owner.ask(0x6504, []byte{EntryAeug})
	guest.ask(0x6504, []byte{EntryTitans})
	r := guest.ask(0x6412, u16(roomID)).Reader()
	if r.Read16() != roomID || r.Read16() != 1 || r.Read16() != 1 {
		t.Errorf("room entry user count")
	}
	owner.send(NewClientNotice(0x6508))

	want := NewRule()
	want.Set(battleTime, battleTimeElem)
	if bytes.Equal(want.Serialize(), NewRule().Serialize()) {
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

func enterRoom(c *testClient, roomID uint16, pass string) *LbsMessage {
	q := NewClientQuestion(0x6406)
	q.Writer().Write(append(u16(roomID), encryptString(q.Seq, pass)...))
	c.send(q)
	return c.expect(0x6406, CategoryAnswer)
}

func TestLobbyRoomPassword(t *testing.T) {
	UseTestDB(t)
	addr := startLobby(t, NewMcs())

	const lobbyID, locked, open = 4, 1, 2
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

	if b := owner.ask(0x6602, u16(locked)).Body; !bytes.Equal(b, []byte{1}) {
		t.Errorf("password permission % x", b)
	}
	owner.ask(0x6407, u16(locked))
	q := NewClientQuestion(0x660A)
	q.Writer().Write(encryptString(q.Seq, "gp03"))
	owner.send(q)
	owner.expect(0x660A, CategoryAnswer)
	owner.ask(0x660C, nil)

	if b := guest.expect(0x6405, CategoryNotice).Body; !bytes.Equal(b, []byte{0, locked, 1}) {
		t.Errorf("password notice % x", b)
	}
	if b := guest.ask(0x6405, u16(locked)).Body; !bytes.Equal(b, []byte{0, locked, 1}) {
		t.Errorf("password info % x", b)
	}

	for _, pass := range []string{"gp04", ""} {
		a := enterRoom(guest, locked, pass)
		if a.Status != StatusError {
			t.Fatalf("password %q: status %x", pass, a.Status)
		}
		if msg, _ := japanese.ShiftJIS.NewDecoder().String(a.Reader().ReadString()); msg != "<B>パスワードが違います<B>" {
			t.Errorf("password %q: message %q", pass, msg)
		}
	}
	if r := owner.ask(0x6403, u16(locked)).Reader(); r.Read16() != locked || r.Read16() != 1 {
		t.Errorf("user count after wrong passwords")
	}

	if a := enterRoom(guest, locked, "gp03"); a.Status != StatusSuccess || len(a.Body) != 0 {
		t.Fatalf("right password: status %x body % x", a.Status, a.Body)
	}
	if joined := owner.expect(0x6503, CategoryNotice).Reader().ReadString(); joined != ids[1] {
		t.Errorf("owner saw %q join", joined)
	}
	if r := owner.ask(0x6403, u16(locked)).Reader(); r.Read16() != locked || r.Read16() != 2 {
		t.Errorf("user count after the right password")
	}

	guest.ask(0x6501, nil)
	guest.ask(0x6407, u16(open))
	guest.ask(0x660C, nil)
	if b := owner.expect(0x6405, CategoryNotice).Body; !bytes.Equal(b, []byte{0, locked, 1}) {
		t.Errorf("own room password notice % x", b)
	}
	if b := owner.expect(0x6405, CategoryNotice).Body; !bytes.Equal(b, []byte{0, open, 0}) {
		t.Errorf("open room password notice % x", b)
	}
	if b := owner.ask(0x6405, u16(open)).Body; !bytes.Equal(b, []byte{0, open, 0}) {
		t.Errorf("open room password info % x", b)
	}
	owner.ask(0x6501, nil)
	if a := enterRoom(owner, open, "gp03"); a.Status != StatusSuccess {
		t.Errorf("open room with a leftover password: status %x", a.Status)
	}
	if r := owner.ask(0x6403, u16(open)).Reader(); r.Read16() != open || r.Read16() != 2 {
		t.Errorf("open room user count")
	}
}

// waitPeers waits until the lobby serves n peers.
func waitPeers(t *testing.T, app *Lbs, n int) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		got := 0
		app.Locked(func(a *Lbs) { got = len(a.peers) })
		if got == n {
			return
		}
	}
	t.Fatalf("lobby did not reach %d peers", n)
}

// expectClosed reads until the lobby closes the connection.
func expectClosed(t *testing.T, c *testClient) {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	for {
		_, err := c.conn.Read(buf)
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			t.Fatalf("want the lobby to close the connection, got %v", err)
		}
		return
	}
}

func TestAliveCheckIntervalLineCheck(t *testing.T) {
	addr, _ := startLobbyAppWith(t, NewMcs(), func(a *Lbs) {
		a.aliveCheckInterval = 50 * time.Millisecond
	})
	c := dialLobby(t, addr)
	c.expect(0x6001, CategoryQuestion)
	c.expect(0x6001, CategoryQuestion)
}

func TestAliveCheckRecvTimeout(t *testing.T) {
	addr, app := startLobbyApp(t, NewMcs())
	c := dialLobby(t, addr)
	waitPeers(t, app, 1)

	var start time.Time
	app.Locked(func(a *Lbs) {
		for _, p := range a.peers {
			start = p.lastRecvTime
		}
		a.checkAlive(start.Add(peerRecvTimeout - time.Second))
	})
	c.expect(0x6001, CategoryQuestion)

	app.Locked(func(a *Lbs) { a.checkAlive(start.Add(peerRecvTimeout)) })
	expectClosed(t, c)
	waitPeers(t, app, 0)
}

func TestAliveCheckBattleTimeout(t *testing.T) {
	_, app := startLobbyApp(t, NewMcs())
	now := time.Now()
	app.Locked(func(a *Lbs) {
		for sid, age := range map[string]time.Duration{"old": battleTimeout, "new": battleTimeout - time.Second} {
			b := NewBattle(PlatformConsole, 0)
			b.StartTime = now.Add(-age)
			a.battles[sid] = b
		}
		a.checkAlive(now)
	})
	app.Locked(func(a *Lbs) {
		if _, ok := a.battles["old"]; ok {
			t.Error("battle of battleTimeout age kept")
		}
		if _, ok := a.battles["new"]; !ok {
			t.Error("younger battle dropped")
		}
	})
}

func newLbsUser(id string) *LbsUser {
	return &LbsUser{DBUser: DBUser{UserID: id}}
}

func TestRuleDefaults(t *testing.T) {
	r := NewRule()
	if RuleCount() != 10 {
		t.Fatalf("RuleCount = %d, want 10", RuleCount())
	}
	for id := byte(0); id < RuleCount(); id++ {
		if r.Get(id) != RuleElementDefault(id) {
			t.Errorf("rule %d: Get = %d, default %d", id, r.Get(id), RuleElementDefault(id))
		}
		if RuleElementDefault(id) >= RuleElementCount(id) {
			t.Errorf("rule %d: default %d out of %d elements", id, RuleElementDefault(id), RuleElementCount(id))
		}
		if RuleTitle(id) == "" || RuleElementName(id, 0) == "" {
			t.Errorf("rule %d: empty title or element name", id)
		}
	}
	if r.playerCount != 4 || r.battleTime != 210 || r.battleGauge != 600 || r.damageLevel != 3 {
		t.Errorf("default rule = %+v", r)
	}
}

func TestRuleSetGet(t *testing.T) {
	r := NewRule()
	r.Set(rulePlayerCount, 0)
	r.Set(ruleBattleCount, 4)
	r.Set(ruleVisibleTeam, 2)
	r.Set(ruleBattleTime, 0)
	r.Set(ruleBattleGauge, 6)
	r.Set(ruleReloadType, 1)
	r.Set(ruleDamageLevel, 0)
	r.Set(ruleAwakeType, 4)
	r.Set(ruleAwakeIncRatio, 0)
	r.Set(ruleAwakeDecRatio, 4)
	want := Rule{playerCount: 2, battleCount: 10, visibleTeam: 2, battleTime: 90, battleGauge: 1200,
		reloadType: 1, damageLevel: 1, awakeType: 4, awakeIncRatio: 10, awakeDecRatio: 200}
	got := *r
	got.selected = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rule = %+v, want %+v", got, want)
	}
	if r.Get(ruleBattleGauge) != 6 || r.Get(ruleAwakeDecRatio) != 4 {
		t.Errorf("Get does not return the selected element")
	}
	if NewRule().Get(ruleBattleGauge) != RuleElementDefault(ruleBattleGauge) {
		t.Errorf("Set leaked into a new rule")
	}
}

func TestRuleSerialize(t *testing.T) {
	r := NewRule()
	r.Set(ruleBattleGauge, 5) // 900
	r.Set(ruleDamageLevel, 3) // 4, sent as 3
	r.Set(ruleBattleCount, 2) // 3
	r.Set(ruleBattleTime, 4)  // 270
	b := r.Serialize()
	if len(b) != 102 || binary.BigEndian.Uint16(b) != 100 {
		t.Fatalf("len = %d, length field = %d; want 102, 100", len(b), binary.BigEndian.Uint16(b))
	}
	for i := 6; i < 70; i++ {
		if b[i] != 0xFF {
			t.Fatalf("MS mask byte %d = %x", i, b[i])
		}
	}
	u16 := func(off int) uint16 { return binary.BigEndian.Uint16(b[off:]) }
	if u16(72) != 900 || u16(74) != 900 || u16(76) != 100 || u16(78) != 100 || u16(80) != 270 {
		t.Errorf("gauge/awake/time = %d %d %d %d %d", u16(72), u16(74), u16(76), u16(78), u16(80))
	}
	if b[86] != 3 || b[87] != 3 || b[100] != 0 || b[101] != 1 {
		t.Errorf("damage %d, battle count %d, lose points %d, zz pilot %d", b[86], b[87], b[100], b[101])
	}
}

func TestRoomLifecycle(t *testing.T) {
	r := NewRoom(2, 1)
	r.Rule.Set(rulePlayerCount, 0) // 2 players
	a, b := newLbsUser("A"), newLbsUser("B")

	r.Enter(a)
	if r.Owner != "A" || r.MaxPlayer != 2 || r.Status != RoomStateRecruit || r.Deadline.IsZero() {
		t.Fatalf("after first enter: %+v", r)
	}
	r.Enter(b)
	if r.Status != RoomStateFull {
		t.Errorf("status = %d, want full", r.Status)
	}
	r.Exit("nobody")
	if len(r.Users) != 2 || r.Status != RoomStateFull {
		t.Errorf("unknown user exit changed the room")
	}
	r.Exit("A")
	if len(r.Users) != 1 || r.Users[0] != b || r.Status != RoomStateRecruit {
		t.Errorf("after exit A: users %v, status %d", r.Users, r.Status)
	}
	r.Exit("B")
	if r.Status != RoomStateEmpty || r.Owner != "" || r.Name != "(空き)" || r.ID != 1 || r.LobbyID != 2 {
		t.Errorf("empty room not reset: %+v", r)
	}
}

func TestRoomBattleStart(t *testing.T) {
	r := NewRoom(2, 1)
	sides := map[string]byte{"a1": EntryAeug, "t1": EntryTitans, "a2": EntryAeug, "n": EntryNone, "a3": EntryAeug}
	order := []string{"a1", "t1", "a2", "n", "a3"}
	for _, id := range order {
		u := newLbsUser(id)
		r.Enter(u)
		r.Entry(u, sides[id])
	}
	if a, b := r.GetEntryUserCount(); a != 3 || b != 1 {
		t.Fatalf("entry count = %d, %d", a, b)
	}
	if r.CanBattleStart() {
		t.Errorf("3 vs 1 can start")
	}
	active, inactive := r.StartBattleUsers()
	ids := func(us []*LbsUser) (s []string) {
		for _, u := range us {
			s = append(s, u.UserID)
		}
		return
	}
	if got := ids(active); len(got) != 3 || got[0] != "a1" || got[1] != "t1" || got[2] != "a2" {
		t.Errorf("active = %v", got)
	}
	if got := ids(inactive); len(got) != 2 || got[0] != "n" || got[1] != "a3" {
		t.Errorf("inactive = %v", got)
	}
	r.Exit("a3")
	if !r.CanBattleStart() {
		t.Errorf("2 vs 1 cannot start")
	}
	r.Entry(r.Users[1], EntryNone)
	if r.CanBattleStart() {
		t.Errorf("2 vs 0 can start")
	}
}

func TestLobbyEntry(t *testing.T) {
	l := NewLobby("ps2", 4)
	if l.RoomCount() != 5 || len(l.Rooms) != 6 || l.Rooms[5].LobbyID != 4 {
		t.Fatalf("rooms: count %d, map %d", l.RoomCount(), len(l.Rooms))
	}
	users := map[string]*LbsUser{}
	for _, id := range []string{"a1", "a2", "a3", "t1", "t2", "x"} {
		users[id] = newLbsUser(id)
		l.Enter(users[id])
	}
	for _, id := range []string{"a1", "t1", "a2", "a3", "t2"} {
		side := byte(EntryAeug)
		if id[0] == 't' {
			side = EntryTitans
		}
		l.Entry(users[id], side)
	}
	if a, b := l.GetEntryUserCount(); a != 3 || b != 2 || !l.CanBattleStart() {
		t.Fatalf("count %d, %d, can start %v", a, b, l.CanBattleStart())
	}
	var got []string
	for _, u := range l.StartBattleUsers() {
		got = append(got, u.UserID)
	}
	if len(got) != 4 || got[0] != "a1" || got[1] != "t1" || got[2] != "a2" || got[3] != "t2" {
		t.Errorf("StartBattleUsers = %v", got)
	}

	l.Entry(users["t2"], EntryNone)
	if users["t2"].Entry != EntryNone || len(l.EntryUsers) != 4 || l.CanBattleStart() {
		t.Errorf("cancel entry: %v", l.EntryUsers)
	}
	l.Exit("a1")
	l.Exit("nobody")
	if _, ok := l.Users["a1"]; ok || len(l.EntryUsers) != 3 || len(l.Users) != 5 {
		t.Errorf("exit: users %d, entries %v", len(l.Users), l.EntryUsers)
	}
}

func TestRuleSetByKey(t *testing.T) {
	r := NewRule()
	if !r.SetByKey("battle_count", 2) || r.battleCount != 2 {
		t.Errorf("battle_count outside the room menu: %d", r.battleCount)
	}
	if !r.SetByKey("battle_gauge", 450) || r.battleGauge != 450 {
		t.Errorf("battle_gauge outside the room menu: %d", r.battleGauge)
	}
	if r.SetByKey("battle_count", 256) || r.SetByKey("damage_level", 0) || r.SetByKey("nosuch", 1) {
		t.Error("a value that does not fit was set")
	}
}

func TestLobbyCanBattleStart(t *testing.T) {
	cases := []struct {
		start string
		a, b  int
		want  bool
	}{
		{Start1v1, 1, 1, true}, {Start1v1, 1, 0, false},
		{StartSolo, 1, 0, true}, {StartSolo, 0, 1, true}, {StartSolo, 0, 0, false},
		{Start2v2, 1, 1, false}, {Start2v2, 2, 1, false}, {Start2v2, 2, 2, true},
		{"", 1, 1, false}, {"", 1, 0, false}, {"", 2, 2, true},
	}
	for _, c := range cases {
		l := NewLobby("ps2", 2)
		l.Start = c.start
		add := func(n int, side byte, p string) {
			for i := 0; i < n; i++ {
				u := newLbsUser(p + string(rune('0'+i)))
				l.Enter(u)
				l.Entry(u, side)
			}
		}
		add(c.a, EntryAeug, "a")
		add(c.b, EntryTitans, "t")
		if got := l.CanBattleStart(); got != c.want {
			t.Errorf("start %q, %d vs %d: CanBattleStart = %v", c.start, c.a, c.b, got)
		}
	}
}

func TestBattle(t *testing.T) {
	b := NewBattle("ps2", 4)
	l := NewLobby("ps2", 4)
	a1, t1, n := newLbsUser("a1"), newLbsUser("t1"), newLbsUser("n")
	a1.Entry, t1.Entry = EntryAeug, EntryTitans
	a1.Lobby = l
	for _, u := range []*LbsUser{a1, t1, n} {
		b.Add(u)
	}
	if b.Users[0].Lobby != nil || a1.Lobby != l {
		t.Errorf("Add must store a detached copy")
	}
	if b.NumOfEntryUsers() != 2 || len(b.AeugIDs) != 1 || len(b.TitansIDs) != 1 {
		t.Errorf("entries: %v %v", b.AeugIDs, b.TitansIDs)
	}
	if b.GetPosition("t1") != 2 || b.GetPosition("zz") != 0 {
		t.Errorf("GetPosition t1 = %d, zz = %d", b.GetPosition("t1"), b.GetPosition("zz"))
	}
	if u := b.GetUserByPos(1); u == nil || u.UserID != "a1" {
		t.Errorf("GetUserByPos(1) = %v", u)
	}
	b.SetBattleServer(net.IPv4(10, 0, 0, 1), 9000)
	r := NewRule()
	b.SetRule(r)
	if !b.ServerIP.Equal(net.IPv4(10, 0, 0, 1)) || b.ServerPort != 9000 || b.Rule != r {
		t.Errorf("server/rule not set")
	}
}

func TestDecodeSessionID(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"0123505679", "12345678"},
		{"123505679", "12345678"}, // the lobby key1 may come unpadded
		{"0876604322", "87654321"},
		{"0000100001", "00000000"},
		{"9999999999", "99989998"},
	} {
		got, err := DecodeSessionID(tc.value)
		if err != nil || got != tc.want {
			t.Errorf("DecodeSessionID(%q) = %q, %v; want %q", tc.value, got, err, tc.want)
		}
	}
	for _, value := range []string{"", "01235056790", "01235x5679", "-123505679", "0000100000", "0000000000"} {
		if got, err := DecodeSessionID(value); err == nil {
			t.Errorf("DecodeSessionID(%q) = %q, want an error", value, got)
		}
	}
}

func TestPlatformFromInfo(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"emulator=pcsx2\nversion=v2.8.2\nos=windows\ncpu=x86/64\n", PlatformEmu},
		{"emulator=pcsx2\ncpu=arm64\n", PlatformEmu},
		{"emulator=pcsx2\n", PlatformEmu},
		{"os=windows\n", PlatformConsole},
		{"", PlatformConsole},
	}
	for _, c := range cases {
		if got := PlatformFromInfo(ParsePlatformInfo(c.body)); got != c.want {
			t.Errorf("PlatformFromInfo(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}
