package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/text/encoding/japanese"
)

func Test_ParseBattleResult(t *testing.T) {
	type args struct {
		m *LbsMessage
	}
	tests := []struct {
		name string
		args args
	}{
		{"test1-1p", args{&LbsMessage{
			Direction: ClientToServer,
			Category:  CategoryAnswer,
			Command:   0x6138,
			BodySize:  69,
			Seq:       1258,
			Status:    StatusSuccess,
			Body: []byte{
				0x67, 0x89, 0x00, 0x11, 0x00, 0x96, 0x5c, 0x5f, 0x53, 0x6f, 0x77, 0x69, 0x64, 0x63, 0x65, 0x65,
				0x68, 0x67, 0x7f, 0x61, 0x6c, 0x00, 0x00, 0x02, 0x01, 0x03, 0x01, 0x02, 0x00, 0x00, 0x00, 0x02,
				0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x04, 0x95, 0x00, 0x00, 0x0a, 0x5e, 0x00, 0x00, 0x19, 0x82,
				0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x06, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x08, 0x00, 0x00, 0x00,
				0x3c, 0x00, 0x00, 0x03, 0xe7,
			},
		}}},
		{"test1-2p", args{&LbsMessage{
			Direction: ClientToServer,
			Category:  CategoryAnswer,
			Command:   0x6138,
			BodySize:  69,
			Seq:       1256,
			Status:    StatusSuccess,
			Body: []byte{
				0x6a, 0x61, 0x79, 0x63, 0x6e, 0x00, 0x00, 0x02, 0x01, 0x03, 0x01, 0x02, 0x00, 0x00, 0x00, 0x01,
				0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x04, 0x95, 0x00, 0x00, 0x07, 0xc1, 0x00, 0x00, 0x0f, 0xb0,
				0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x06, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x08, 0x00, 0x00, 0x00,
				0x38, 0x00, 0x00, 0x03, 0xe7,
			},
		}}},
		{"test1-3p", args{&LbsMessage{
			Direction: ClientToServer,
			Category:  CategoryAnswer,
			Command:   0x6138,
			BodySize:  69,
			Seq:       1252,
			Status:    StatusSuccess,
			Body: []byte{
				0x67, 0x89, 0x00, 0x11, 0x00, 0x96, 0x66, 0x61, 0x6d, 0x55, 0x4d, 0x6f, 0x62, 0x69, 0x6f, 0x6b,
				0x66, 0x6d, 0x75, 0x67, 0x6a, 0x00, 0x00, 0x02, 0x01, 0x03, 0x02, 0x01, 0x00, 0x00, 0x00, 0x02,
				0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x04, 0x95, 0x00, 0x00, 0x07, 0xd8, 0x00, 0x00, 0x12, 0x48,
				0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x06, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x08, 0x00, 0x00, 0x00,
				0x3e, 0x00, 0x00, 0x03, 0xe7,
			},
		}}},
		{"test1-4p", args{&LbsMessage{
			Direction: ClientToServer,
			Category:  CategoryAnswer,
			Command:   0x6138,
			BodySize:  69,
			Seq:       1249,
			Status:    StatusSuccess,
			Body: []byte{
				0x67, 0x89, 0x00, 0x11, 0x00, 0x96, 0x65, 0x64, 0x6a, 0x50, 0x4e, 0x52, 0x5d, 0x54, 0x6c, 0x6e,
				0x61, 0x68, 0x76, 0x6a, 0x65, 0x00, 0x00, 0x02, 0x01, 0x03, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01,
				0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x04, 0x95, 0x00, 0x00, 0x07, 0xb0, 0x00, 0x00, 0x10, 0x14,
				0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x06, 0x00, 0x00, 0x00, 0x0f, 0x00, 0x08, 0x00, 0x00, 0x00,
				0x3a, 0x00, 0x00, 0x03, 0xe7,
			},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parseBattleResult(tt.args.m)
		})
	}
}

// newCapturePeer returns a peer whose sent messages stay in its Conn's outbuf.
func newCapturePeer(t *testing.T) *LbsPeer {
	t.Helper()
	c, _ := newPipe(t)
	return &LbsPeer{app: &Lbs{}, conn: NewLbsConn(c)}
}

func sentMessage(t *testing.T, p *LbsPeer) *LbsMessage {
	t.Helper()
	n, m := Deserialize(p.conn.outbuf)
	if m == nil || n != len(p.conn.outbuf) {
		t.Fatalf("want one sent message, outbuf % x", p.conn.outbuf)
	}
	return m
}

func TestGetBattleOpponentStatus(t *testing.T) {
	b := NewBattle(PlatformConsole, 2)
	b.Add(&LbsUser{DBUser: DBUser{UserID: "A", WinCount: 1500}, Entry: EntryAeug})
	b.Add(&LbsUser{DBUser: DBUser{UserID: "B"}, Entry: EntryTitans})
	handler := defaultHandlers.handlers[0x6917]

	cases := []struct {
		name   string
		battle *LbsBattle
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
	}
}

func newUDPTestPeer(info map[string]string, test bool) *LbsPeer {
	b := NewBattle(PlatformFromInfo(info), 2)
	b.ServerIP = net.IPv4(192, 168, 1, 8)
	b.ServerPort = 8210
	b.TestBattle = test
	for _, id := range []string{"AAAAAA", "BBBBBB"} {
		b.Users = append(b.Users, LbsUser{DBUser: DBUser{UserID: id, SessionID: "S" + id}})
	}
	p := &LbsPeer{PlatformInfo: info, Platform: PlatformFromInfo(info)}
	p.UserID = "BBBBBB"
	p.Battle = b
	return p
}

func battleInfo(t *testing.T, n *LbsMessage) *P2PMatching {
	t.Helper()
	if n == nil {
		t.Fatal("no notice")
	}
	m := &P2PMatching{}
	if err := json.Unmarshal(n.Body, m); err != nil {
		t.Fatal(err)
	}
	return m
}

func wantBattleInfo(t *testing.T, got, want *P2PMatching) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("battle info\n%+v\nwant\n%+v", got, want)
	}
}

// wireFixture checks body against testdata/wire/name, the JSON shared with the client's
// tests; ZDXSV_WRITE_TESTDATA=1 writes it.
func wireFixture(t *testing.T, name string, body []byte) {
	t.Helper()
	path := "../testdata/wire/" + name
	if os.Getenv("ZDXSV_WRITE_TESTDATA") == "1" {
		if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil || strings.TrimSuffix(string(want), "\n") != string(body) {
		t.Errorf("body differs from %s (%v):\n%s", path, err, body)
	}
}

func testUsers(ids ...string) []P2PUser {
	var us []P2PUser
	for _, id := range ids {
		us = append(us, P2PUser{UserID: id})
	}
	return us
}

func TestNoBattleInfoNotice(t *testing.T) {
	emu := map[string]string{"emulator": "pcsx2", "cpu": "x86/64", "udp": "1"}
	noUDP := map[string]string{"emulator": "pcsx2", "cpu": "x86/64"}
	for name, p := range map[string]*LbsPeer{
		"console":     newUDPTestPeer(map[string]string{}, false),
		"no udp":      newUDPTestPeer(noUDP, false),
		"test battle": newUDPTestPeer(emu, true),
	} {
		if testBattleInfoNotice(p, nil) != nil {
			t.Errorf("%s: notice sent", name)
		}
	}
}

func TestBattleInfoNoticeAlone(t *testing.T) {
	alone := func(info map[string]string) *P2PMatching {
		p := newUDPTestPeer(info, false)
		p.Battle.Users = p.Battle.Users[1:]
		return battleInfo(t, testBattleInfoNotice(p, func(string) map[string]string { return info }))
	}
	m := alone(map[string]string{"emulator": "pcsx2", "udp": "1", "ggpo": "7100"})
	if m.SessionID == 0 || m.PingTestDuration != 0 || len(m.Users) != 1 {
		t.Errorf("alone with a GGPO port: %+v", m)
	}
	if m := alone(map[string]string{"emulator": "pcsx2", "udp": "1"}); m.SessionID != 0 {
		t.Errorf("alone without a GGPO port: %+v", m)
	}
}

func TestBattleInfoNoticeP2P(t *testing.T) {
	emu := map[string]string{"emulator": "pcsx2", "udp": "1", "sync": "0123456789abcdef"}
	p := newUDPTestPeer(emu, false)
	p.Battle.Users = append(p.Battle.Users,
		LbsUser{DBUser: DBUser{UserID: "CCCCCC", SessionID: "SCCCCCC"}},
		LbsUser{DBUser: DBUser{UserID: "DDDDDD", SessionID: "SDDDDDD"}})
	infos := map[string]map[string]string{
		// every address (IPv4 public, local, IPv6); the TCP source IP is udp_addr's: not added
		"AAAAAA": {"udp": "1", "udp_addr": "203.0.113.5:40001", "udp_local": "192.168.1.20:40001", "udp_addr6": "[2001:db8::5]:40001", "ggpo": "7001", "tcp_ip": "203.0.113.5", "sync": "fedcba9876543210"},
		// self: never listed
		"BBBBBB": {"udp": "1", "udp_addr": "203.0.113.6:40002"},
		// local IPv4 only (no STUN answer), and IPv6; its TCP source IP with the local port;
		// a bad port, a bad ggpo port, a bad fingerprint: dropped
		"CCCCCC": {"udp": "1", "udp_local": "192.168.1.21:40003", "udp_addr6": "[2001:db8::21]:40003", "udp_addr": "203.0.113.9:0", "ggpo": "70000", "tcp_ip": "198.51.100.9", "sync": "FEDCBA9876543210"},
		// not a bridge: no candidates even with an address
		"DDDDDD": {"udp_addr": "203.0.113.7:40004", "ggpo": "7004"},
	}
	p.Battle.BattleCode = "1696492800000"
	p.Battle.GgpoSession = 1462212142 // NewBattle draws a random one
	// names as login stores them (ReadEncryptedString already decoded Shift-JIS):
	// "アムロ", a control byte (dropped), none
	p.Battle.Users[0].Name = "アムロ"
	p.Battle.Users[1].Name = "Bob\n"
	// a pilot name as login stores the game's user binary (captured, NULs at both ends trimmed)
	p.Battle.Users[0].Bin = capturedUserBinary
	n := testBattleInfoNotice(p, func(id string) map[string]string { return infos[id] })
	users := testUsers("AAAAAA", "BBBBBB", "CCCCCC", "DDDDDD")
	users[0].UserName, users[0].PilotName, users[0].GgpoPort, users[0].Sync = "アムロ", "カミーユ・ビダン", 7001, "fedcba9876543210"
	users[1].UserName, users[1].Sync = "Bob", "0123456789abcdef"
	cand := func(id string, peer int, ip string, port int) PlayerAddress {
		return PlayerAddress{UserID: id, PeerID: peer, IP: ip, Port: port}
	}
	wantBattleInfo(t, battleInfo(t, n), &P2PMatching{
		BattleCode: "1696492800000", SessionID: 1462212142, PeerID: 1, PingTestDuration: 7500,
		Users: users,
		Candidates: []PlayerAddress{
			cand("AAAAAA", 0, "203.0.113.5", 40001), cand("AAAAAA", 0, "192.168.1.20", 40001), cand("AAAAAA", 0, "2001:db8::5", 40001),
			cand("CCCCCC", 2, "192.168.1.21", 40003), cand("CCCCCC", 2, "2001:db8::21", 40003), cand("CCCCCC", 2, "198.51.100.9", 40003),
		},
		LobbySessionID: "SBBBBBB", BattleServer: "192.168.1.8:8210"})
	wireFixture(t, "battle_info.json", n.Body)
}

// capturedUserBinary is a captured SetUserBinary body after ReadEncryptedString:
// five 22-byte Shift-JIS fields (pilot name, four quick chat messages), NUL padded.
var capturedUserBinary = "カミーユ・ビダン" + strings.Repeat("\x00", 6) +
	"了解！" + strings.Repeat("\x00", 16) +
	"そちらの被害状況は？" + strings.Repeat("\x00", 2) +
	"敵機撃破！" + strings.Repeat("\x00", 12) +
	"俺に任せろ！"

func TestPilotName(t *testing.T) {
	for _, c := range []struct{ bin, want string }{
		{capturedUserBinary, "カミーユ・ビダン"},
		{"", ""},
		// a full field (11 two-byte characters, no NUL) ends at its 22 bytes
		{strings.Repeat("ア", 11) + "了解！", strings.Repeat("ア", 11)},
		// ASCII and control characters
		{"Amuro\t\x00x", "Amuro"},
	} {
		if got := pilotName(c.bin); got != c.want {
			t.Errorf("pilotName(%q) = %q want %q", c.bin, got, c.want)
		}
	}
}

// testdata/wire/matching_report.json is logged as received; its round_data saves nothing.
func TestP2PMatchingReportLogOnly(t *testing.T) {
	UseTestDB(t)
	body, err := os.ReadFile("../testdata/wire/matching_report.json")
	if err != nil {
		t.Fatal(err)
	}
	for pos, id := range []string{"AAAAAA", "BBBBBB"} {
		if err := DefaultDB.AddBattleRecord(&BattleRecord{BattleCode: "1696492800000", UserID: id, Players: 2, Pos: pos + 1, Side: pos + 1}); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	saved := logger
	logger = zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&buf), zap.InfoLevel))
	defer func() { logger = saved }()

	p := &LbsPeer{PlatformInfo: map[string]string{"emulator": "pcsx2"}}
	m := NewClientNotice(0x9952)
	m.Writer().Write(body)
	defaultHandlers.handlers[0x9952](p, m)

	var line struct{ Report any }
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	var want any
	json.Unmarshal(body, &want)
	if !reflect.DeepEqual(line.Report, want) {
		t.Errorf("logged %s", buf.String())
	}
	for _, id := range []string{"AAAAAA", "BBBBBB"} {
		rec, err := DefaultDB.GetBattleRecordUser("1696492800000", id)
		if err != nil {
			t.Fatal(err)
		}
		if rec.UsedMsList != "" || rec.UsedMsMask != 0 {
			t.Errorf("%s: used_ms %q %x", id, rec.UsedMsList, rec.UsedMsMask)
		}
	}
}

// testdata/wire/battle_info_full.json has every battle info field; the struct keeps all of them.
func TestBattleInfoFullFixture(t *testing.T) {
	body, err := os.ReadFile("../testdata/wire/battle_info_full.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var m P2PMatching
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	if len(m.Relays) != 1 || m.Relays[0].Token != 18364758544493064720 || !m.SpectatorUplink || m.Users[1].Team != 2 {
		t.Errorf("decoded %+v", m)
	}
	again, _ := json.Marshal(m)
	var a, b any
	json.Unmarshal(body, &a)
	json.Unmarshal(again, &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("encoded again: %s", again)
	}
}

// The log line carries the body as received: fields the server does not know, and a
// body it cannot decode.
func TestP2PMatchingReportLog(t *testing.T) {
	var buf bytes.Buffer
	saved := logger
	logger = zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&buf), zap.InfoLevel))
	defer func() { logger = saved }()
	p := &LbsPeer{PlatformInfo: map[string]string{"emulator": "pcsx2"}}
	p.UserID = "BBBBBB"
	for body, want := range map[string]string{
		`{"user_id":"BBBBBB","added_later":{"x":[1,2]}}`: `"report":{"user_id":"BBBBBB","added_later":{"x":[1,2]}}`,
		`{"user_id":`: `"report":"{\"user_id\":"`,
	} {
		buf.Reset()
		(&Lbs{}).OnP2PMatchingReport(p, []byte(body))
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log for %s:\n%s", body, buf.String())
		}
	}
}

func TestBattleInfoNoticeRelay(t *testing.T) {
	r := NewRelay()
	LobbyRelay, RelayPublicAddr, RelayPublicAddr6 = r, "192.168.1.8:8203", "[2001:db8::8]:8204"
	defer func() { LobbyRelay, RelayPublicAddr, RelayPublicAddr6 = nil, "", "" }()
	emu := map[string]string{"emulator": "pcsx2", "udp": "1", "relay_server": "1"}
	p := newUDPTestPeer(emu, false)
	p.Battle.BattleCode = "1696492800000"
	p.Battle.GgpoSession = 1462212142
	other := map[string]string{"udp": "1", "udp_addr": "203.0.113.5:40001", "ggpo": "7001", "relay_server": "1"}
	info := func(id string) map[string]string { return other }

	// the relay token is derived from the battle's ggpo_session
	want := RelayServer{IP: "192.168.1.8", Port: 8203, Token: r.SessionToken(1462212142),
		IP6: "2001:db8::8", Port6: 8204}
	if got := battleInfo(t, testBattleInfoNotice(p, info)).Relays; len(got) != 1 || got[0] != want {
		t.Fatalf("relays %v, want %v", got, want)
	}
	if len(r.sessions) != 1 {
		t.Fatalf("relay sessions %d, want 1", len(r.sessions))
	}

	// a player without relay support: no relay for the battle
	delete(other, "relay_server")
	if got := battleInfo(t, testBattleInfoNotice(p, info)).Relays; len(got) != 0 {
		t.Fatalf("relay offered to a battle with a player without relay_server=1: %v", got)
	}
	other["relay_server"] = "1"
	p.PlatformInfo = map[string]string{"emulator": "pcsx2", "udp": "1"}
	if got := battleInfo(t, testBattleInfoNotice(p, info)).Relays; len(got) != 0 {
		t.Fatalf("relay offered to a client without relay_server=1: %v", got)
	}
}

func TestLiveUsers(t *testing.T) {
	b := NewBattle("", 0)
	b.Users = []LbsUser{{DBUser: DBUser{UserID: "AAAAAA", Name: "アムロ\n"}, Bin: capturedUserBinary, Entry: EntryTitans},
		{DBUser: DBUser{UserID: "BBBBBB", Name: "Bob"}, Entry: EntryAeug}}
	var got []string
	for _, u := range liveUsers(b) {
		got = append(got, fmt.Sprintf("%s/%s/%s/%d/%d", u.UserID, u.UserName, u.PilotName, u.Team, u.Pos))
	}
	want := []string{fmt.Sprintf("AAAAAA/アムロ/カミーユ・ビダン/%d/1", EntryTitans), fmt.Sprintf("BBBBBB/Bob//%d/2", EntryAeug)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("users %v, want %v", got, want)
	}
}

func testBattleInfoNotice(p *LbsPeer, info func(userID string) map[string]string) *LbsMessage {
	return battleInfoNotice(p2pMatching(p, info))
}

// A player asking for the battle server address after the others left the
// lobby for the battle still gets their battle info.
func TestBattleInfoAfterPeersLeft(t *testing.T) {
	UseTestDB(t)
	addr := startLobby(t, NewMcs())
	const lobbyID = 4
	sides := []byte{EntryAeug, EntryTitans, EntryAeug, EntryTitans}
	var cs []*testClient
	var ids []string
	for i := range sides {
		c := dialLobby(t, addr)
		n := NewClientNotice(0x9950)
		n.Writer().Write([]byte(fmt.Sprintf("emulator=pcsx2\nudp=1\nggpo=%d\nudp_addr=192.0.2.%d:%d\nsync=0123456789abcdef\n",
			20000+i, 10+i, 30000+i)))
		c.send(n)
		id, _, _ := c.login(registerAccount(t), "******", fmt.Sprintf("pilot%d", i))
		if a := c.ask(0x6305, u16(lobbyID)); a.Status != StatusSuccess {
			t.Fatalf("EnterLobby status %x", a.Status)
		}
		cs = append(cs, c)
		ids = append(ids, id)
	}
	for i, c := range cs {
		c.ask(0x640E, []byte{sides[i]})
	}
	for _, c := range cs {
		c.expect(0x6910, CategoryNotice)
	}

	for i, c := range cs {
		c.ask(0x6916, nil)
		m := battleInfo(t, c.expect(0x9951, CategoryCustom))
		peers := map[string]bool{}
		for _, a := range m.Candidates {
			peers[a.UserID] = true
		}
		for _, u := range m.Users {
			if u.Sync != "0123456789abcdef" {
				t.Errorf("player %d: %s sync %q", i, u.UserID, u.Sync)
			}
			if u.UserID == ids[i] {
				continue
			}
			if u.GgpoPort == 0 || !peers[u.UserID] {
				t.Errorf("player %d: %s ggpo port %v, candidates %v", i, u.UserID, u.GgpoPort, peers[u.UserID])
			}
		}
		if m.SessionID == 0 || len(m.Users) != len(cs) {
			t.Errorf("player %d: battle info %+v", i, m)
		}
		c.ask(0x6002, nil)
		c.conn.Close()
	}
}

func TestLobbyBattleSameSyncOnly(t *testing.T) {
	conn := UseTestDB(t)
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('emu', 2, '1v1', '', '', '')`); err != nil {
		t.Fatal(err)
	}
	addr := startLobby(t, NewMcs())
	const lobbyID = 2
	enter := func(i int, sync string, side byte) *testClient {
		c := dialLobby(t, addr)
		n := NewClientNotice(0x9950)
		n.Writer().Write([]byte("emulator=pcsx2\nsync=" + sync + "\n"))
		c.send(n)
		c.login(registerAccount(t), "******", fmt.Sprintf("pilot%d", i))
		if a := c.ask(0x6305, u16(lobbyID)); a.Status != StatusSuccess {
			t.Fatalf("EnterLobby status %x", a.Status)
		}
		c.ask(0x640E, []byte{side})
		return c
	}
	started := func(c *testClient) bool {
		t.Helper()
		c.ask(0x600E, nil)
		for _, m := range c.queue {
			if m.Command == 0x6910 {
				return true
			}
		}
		return false
	}

	a1 := enter(0, "00000000000000aa", EntryAeug)
	b1 := enter(1, "00000000000000bb", EntryTitans)
	if started(a1) || started(b1) {
		t.Fatal("battle between different sync fingerprints")
	}
	a2 := enter(2, "00000000000000aa", EntryTitans)
	for i, c := range []*testClient{a1, a2} {
		c.expect(0x6910, CategoryNotice)
		if n := c.ask(0x6911, nil).Reader().Read8(); n != 2 {
			t.Errorf("player %d: user count %d", i, n)
		}
	}
	if started(b1) {
		t.Fatal("the other fingerprint joined the battle")
	}
}

// lobby1Proxy logs a user in, enters lobby 1 and registers its proxy with the lobby API.
func lobby1Proxy(t *testing.T) (*Lbs, *testClient, *testLogin, string) {
	t.Helper()
	conn := UseTestDB(t)
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('console', 1, '', '', '', 'zproxy_test')`); err != nil {
		t.Fatal(err)
	}
	addr, app := startLobbyApp(t, NewMcs())
	api := newZproxyAPI(app)
	ac := registerAccount(t)
	c := dialLobby(t, addr)
	userID, _, _ := c.login(ac, "******", "pilot")
	c.ask(0x6305, u16(1))
	res := api.register(t, &RegisterProxyRequest{
		CurrentVersion: requiredVersion,
		UserID:         userID,
		LocalIP:        net.IPv4(192, 168, 0, 10),
		Port:           9000,
		UDPAddrs:       []string{"192.168.0.10:9000"},
	})
	if !res.Result {
		t.Fatalf("register proxy: %+v", res)
	}
	return app, c, ac, userID
}

func expectHeard(t *testing.T, c *testClient, line string) {
	t.Helper()
	want, err := japanese.ShiftJIS.NewEncoder().String(line)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.heard(); fmt.Sprint(got) != fmt.Sprint([]string{want}) {
		t.Errorf("heard %q, want %q", got, want)
	}
}

func TestLobbyTestBattle(t *testing.T) {
	app, c, ac, userID := lobby1Proxy(t)
	c.ask(0x640E, []byte{EntryAeug})
	expectHeard(t, c, "SERVER:接続テスト対戦開始")
	c.expect(0x6910, CategoryNotice)
	c.ask(0x6002, nil)

	api := newZproxyAPI(app)
	res := api.battleInfo(t, ac.SessionID)
	if !res.Result || !res.IsTest || len(res.Users) != 1 || res.Users[0].UserID != userID {
		t.Errorf("battle info: %+v", res)
	}
}

func TestLobbyTestBattleStaleProxy(t *testing.T) {
	app, c, _, _ := lobby1Proxy(t)
	app.Locked(func(a *Lbs) {
		for _, p := range a.users {
			p.proxyRegTime = time.Now().Add(-21 * time.Second)
		}
	})
	c.ask(0x640E, []byte{EntryAeug})
	expectHeard(t, c, "SERVER:UDPプロキシが登録されていません")
	for _, m := range c.queue {
		if m.Command == 0x6910 {
			t.Errorf("battle start notice with a proxy registered 21 s ago")
		}
	}
}

// A client leaves for a battle and never comes back with its battle session: the user logs in again
// with a new session, enters a lobby and logs out. /lbs/status must not list them in a battle.
func TestStatusAfterLeftBattleRelogin(t *testing.T) {
	conn := UseTestDB(t)
	if _, err := conn.Exec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES ('console', 1, '', '', '', 'zproxy_test')`); err != nil {
		t.Fatal(err)
	}
	addr, app := startLobbyApp(t, NewMcs())
	api := newZproxyAPI(app)
	ac := registerAccount(t)
	c := dialLobby(t, addr)
	userID, _, _ := c.login(ac, "******", "pilot")
	c.ask(0x6305, u16(1))
	if res := api.register(t, &RegisterProxyRequest{
		CurrentVersion: requiredVersion,
		UserID:         userID,
		LocalIP:        net.IPv4(192, 168, 0, 10),
		Port:           9000,
		UDPAddrs:       []string{"192.168.0.10:9000"},
	}); !res.Result {
		t.Fatalf("register proxy: %+v", res)
	}
	c.ask(0x640E, []byte{EntryAeug})
	c.expect(0x6910, CategoryNotice)
	c.ask(0x6002, nil)
	c.conn.Close()

	c = dialLobby(t, addr)
	if got, _, _ := c.login(loginAgain(ac.LoginKey), userID, "pilot"); got != userID {
		t.Fatalf("relogin as %q", got)
	}
	c.ask(0x6305, u16(1))
	if s := MakeStatus(app.Status(), time.Now()); s.LobbyUserCount != 1 || s.BattleUserCount != 0 {
		t.Fatalf("in the lobby: %+v", s)
	}

	c.ask(0x6006, nil)
	c.conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := MakeStatus(app.Status(), time.Now())
		if s.LobbyUserCount == 0 {
			if s.BattleUserCount != 0 {
				t.Fatalf("after the logout: %+v", s)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("still in the lobby: %+v", s)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The replay upload's used_ms sets every player's used_ms once the replay is stored;
// /lbs/replay?used_ms= finds it. A second upload (409), a malformed or a missing field saves nothing.
func TestReplayUploadUsedMsSearch(t *testing.T) {
	UseTestDB(t)
	for _, r := range []*BattleRecord{
		{BattleCode: "B1", UserID: "AAAAAA", Players: 2, Pos: 1, Side: 1},
		{BattleCode: "B1", UserID: "BBBBBB", Players: 2, Pos: 2, Side: 2},
		{BattleCode: "B2", UserID: "AAAAAA", Players: 2, Pos: 1, Side: 1},
		{BattleCode: "B2", UserID: "CCCCCC", Players: 2, Pos: 2, Side: 2},
		{BattleCode: "B3", UserID: "AAAAAA", Players: 2, Pos: 1, Side: 1},
		{BattleCode: "B4", UserID: "AAAAAA", Players: 2, Pos: 1, Side: 1},
	} {
		if err := DefaultDB.AddBattleRecord(r); err != nil {
			t.Fatal(err)
		}
	}

	h := &APIHandler{ReplayStore: &DirReplayStore{Dir: t.TempDir(), URL: "http://127.0.0.1/lbs/"}}
	upload := func(code string, wantCode int, fields ...string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, replayUploadRequest(t, code+".pb", []byte("replay"), fields...))
		if w.Code != wantCode {
			t.Errorf("upload %s %v: code %d body %s", code, fields, w.Code, w.Body.String())
		}
	}
	upload("B1", 200, "used_ms", "[[1,9],[4,9],[1]]")
	upload("B1", http.StatusConflict, "used_ms", "[[30,30]]")
	upload("B2", 200, "used_ms", "[[2,64]]")
	upload("B3", 200, "used_ms", `[[1,"x"]]`)
	upload("B4", 200)
	upload("B9", 200, "used_ms", "[[5,5]]")

	search := func(query string) (codes []string, users map[string]string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/lbs/replay?"+query, nil))
		var found []*FoundReplay
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &found); err != nil {
				t.Fatal(err)
			}
		}
		users = map[string]string{}
		for _, f := range found {
			codes = append(codes, f.BattleCode)
			for _, u := range f.Users {
				users[f.BattleCode+"/"+u.UserID] = u.UsedMsList
			}
		}
		return codes, users
	}

	_, users := search("")
	want := map[string]string{"B1/AAAAAA": "1,4,1", "B1/BBBBBB": "9,9,0", "B2/AAAAAA": "2", "B2/CCCCCC": "0", "B3/AAAAAA": "", "B4/AAAAAA": ""}
	if !reflect.DeepEqual(want, users) {
		t.Errorf("used_ms_list %v, want %v", users, want)
	}
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"used_ms=4", []string{"B1"}},
		{"used_ms=9", []string{"B1"}},
		{"used_ms=30", nil},
		{"used_ms=64", nil},
		{"used_ms=2", []string{"B2"}},
		{"used_ms=9&user_id=AAAAAA", nil},
		{"used_ms=9&user_id=BBBBBB", []string{"B1"}},
		{"used_ms=1&user_id=AAAAAA&user_id=CCCCCC", []string{"B1"}},
	} {
		if codes, _ := search(c.query); !reflect.DeepEqual(c.want, codes) {
			t.Errorf("%s: %v, want %v", c.query, codes, c.want)
		}
	}
}

func TestBattleGetUserByPosOutOfRange(t *testing.T) {
	b := NewBattle("ps2", 2)
	b.Add(&LbsUser{DBUser: DBUser{UserID: "a"}})
	b.Add(&LbsUser{DBUser: DBUser{UserID: "b"}})
	for _, pos := range []byte{0, 3, 255} {
		if u := b.GetUserByPos(pos); u != nil {
			t.Errorf("GetUserByPos(%d) = %v, want nil", pos, u.UserID)
		}
	}
	if u := b.GetUserByPos(2); u == nil || u.UserID != "b" {
		t.Errorf("GetUserByPos(2) = %v", u)
	}
}
