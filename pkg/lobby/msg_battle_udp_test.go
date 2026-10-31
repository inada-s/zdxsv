package lobby

import (
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"zdxsv/pkg/db"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
	"google.golang.org/protobuf/encoding/prototext"
)

func newUDPTestPeer(info map[string]string, test bool) *AppPeer {
	b := model.NewBattle(model.PlatformFromInfo(info), 2)
	b.ServerIP = net.IPv4(192, 168, 1, 8)
	b.ServerPort = 8210
	b.TestBattle = test
	for _, id := range []string{"AAAAAA", "BBBBBB"} {
		b.Users = append(b.Users, model.User{User: db.User{UserID: id, SessionID: "S" + id}})
	}
	p := &AppPeer{PlatformInfo: info, Platform: model.PlatformFromInfo(info)}
	p.UserID = "BBBBBB"
	p.Battle = b
	return p
}

func battleInfo(t *testing.T, n *Message) *proto.P2PMatching {
	t.Helper()
	if n == nil {
		t.Fatal("no notice")
	}
	m := &proto.P2PMatching{}
	if err := pb.Unmarshal(n.Body, m); err != nil {
		t.Fatal(err)
	}
	return m
}

func wantBattleInfo(t *testing.T, got, want *proto.P2PMatching) {
	t.Helper()
	if !pb.Equal(got, want) {
		t.Fatalf("battle info\n%v\nwant\n%v", prototext.Format(got), prototext.Format(want))
	}
}

func testUsers(ids ...string) []*proto.P2PUser {
	var us []*proto.P2PUser
	for _, id := range ids {
		us = append(us, &proto.P2PUser{UserId: pb.String(id)})
	}
	return us
}

func TestBattleInfoNotice(t *testing.T) {
	emu := map[string]string{"emulator": "pcsx2", "cpu": "x86/64", "udp": "1"}
	n := battleInfoNotice(newUDPTestPeer(emu, false), nil)
	if n != nil && (n.Direction != ServerToClient || n.Category != CategoryCustom || n.Command != 0x9951) {
		t.Errorf("header %x %x %x", n.Direction, n.Category, n.Command)
	}
	wantBattleInfo(t, battleInfo(t, n), &proto.P2PMatching{
		BattleCode: pb.String(""), PeerId: pb.Int32(1), LobbySessionId: pb.String("SBBBBBB"),
		BattleServer: pb.String("192.168.1.8:8210"), Users: testUsers("AAAAAA", "BBBBBB")})

	p := newUDPTestPeer(emu, false)
	p.Battle.Users[0].Entry = model.EntryTitans
	p.Battle.Users[1].Entry = model.EntryAeug
	users := testUsers("AAAAAA", "BBBBBB")
	users[0].Team = pb.Int32(2)
	users[1].Team = pb.Int32(1)
	wantBattleInfo(t, battleInfo(t, battleInfoNotice(p, nil)), &proto.P2PMatching{
		BattleCode: pb.String(""), PeerId: pb.Int32(1), LobbySessionId: pb.String("SBBBBBB"),
		BattleServer: pb.String("192.168.1.8:8210"), Users: users})

	noUDP := map[string]string{"emulator": "pcsx2", "cpu": "x86/64"}
	for name, p := range map[string]*AppPeer{
		"console":     newUDPTestPeer(map[string]string{}, false),
		"no udp":      newUDPTestPeer(noUDP, false),
		"test battle": newUDPTestPeer(emu, true),
	} {
		if battleInfoNotice(p, nil) != nil {
			t.Errorf("%s: notice sent", name)
		}
	}
}

// testdata/p2p_matching.bin is the body this test builds; the client's battle info
// decoder is tested against the same bytes.
func TestBattleInfoNoticeP2P(t *testing.T) {
	emu := map[string]string{"emulator": "pcsx2", "udp": "1", "sync": "0123456789abcdef"}
	p := newUDPTestPeer(emu, false)
	p.Battle.Users = append(p.Battle.Users,
		model.User{User: db.User{UserID: "CCCCCC", SessionID: "SCCCCCC"}},
		model.User{User: db.User{UserID: "DDDDDD", SessionID: "SDDDDDD"}})
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
	n := battleInfoNotice(p, func(id string) map[string]string { return infos[id] })
	users := testUsers("AAAAAA", "BBBBBB", "CCCCCC", "DDDDDD")
	users[0].UserName, users[0].PilotName, users[0].GgpoPort, users[0].Sync = pb.String("アムロ"), pb.String("カミーユ・ビダン"), pb.Int32(7001), pb.String("fedcba9876543210")
	users[1].UserName, users[1].Sync = pb.String("Bob"), pb.String("0123456789abcdef")
	cand := func(id string, peer int32, ip string, port int32) *proto.PlayerAddress {
		return &proto.PlayerAddress{UserId: pb.String(id), PeerId: pb.Int32(peer), Ip: pb.String(ip), Port: pb.Int32(port)}
	}
	wantBattleInfo(t, battleInfo(t, n), &proto.P2PMatching{
		BattleCode: pb.String("1696492800000"), SessionId: pb.Uint32(1462212142), PeerId: pb.Int32(1), PingTestDuration: pb.Int32(7500),
		Users: users,
		Candidates: []*proto.PlayerAddress{
			cand("AAAAAA", 0, "203.0.113.5", 40001), cand("AAAAAA", 0, "192.168.1.20", 40001), cand("AAAAAA", 0, "2001:db8::5", 40001),
			cand("CCCCCC", 2, "192.168.1.21", 40003), cand("CCCCCC", 2, "2001:db8::21", 40003), cand("CCCCCC", 2, "198.51.100.9", 40003),
		},
		LobbySessionId: pb.String("SBBBBBB"), BattleServer: pb.String("192.168.1.8:8210")})
	if os.Getenv("ZDXSV_WRITE_TESTDATA") == "1" {
		if err := os.WriteFile("testdata/p2p_matching.bin", n.Body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile("testdata/p2p_matching.bin")
	if err != nil || string(golden) != string(n.Body) {
		t.Errorf("body differs from testdata/p2p_matching.bin (%v):\n%x", err, n.Body)
	}
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

// testdata/p2p_matching_report.bin is a report encoded by the client's codec.
func TestP2PMatchingReport(t *testing.T) {
	body, err := os.ReadFile("testdata/p2p_matching_report.bin")
	if err != nil {
		t.Fatal(err)
	}
	got := &proto.P2PMatchingReport{}
	if err := pb.Unmarshal(body, got); err != nil {
		t.Fatal(err)
	}
	want := &proto.P2PMatchingReport{
		BattleCode: pb.String("1696492800000"), UserId: pb.String("BBBBBB"), Result: pb.String("ggpo"),
		PeerId: pb.Int32(1), PlayerCount: pb.Int32(2), InputDelay: pb.Int32(2), MinDelay: pb.Int32(1),
		RemotePeers: []*proto.RemotePeerReport{{PeerId: pb.Int32(0), RttMs: pb.Int32(12), Addr: pb.String("203.0.113.5:7001"), Route: pb.String("direct"),
			PingMinMs: pb.Int32(201), PingMedianMs: pb.Int32(215), PingMaxMs: pb.Int32(250), RecvLoss: pb.Int32(3), SendLoss: pb.Int32(5)}},
		RelayCount: pb.Int32(0), PingWaitMs: pb.Int32(1500), CloseReason: pb.String("net battle end"), FrameCount: pb.Int32(21563),
		RollbackFrames: pb.Int32(812), Mismatches: pb.Int32(0), Disconnected: pb.Bool(true), DisconnectedPeerIds: []int32{0},
		TotalTimesync: pb.Int32(17), InputBlockCount: pb.Int32(42), FpsHistory: []int32{60, 59, 60}}
	if !pb.Equal(got, want) {
		t.Errorf("report\n%v\nwant\n%v", prototext.Format(got), prototext.Format(want))
	}
	if line := p2pMatchingReportLine(got); !strings.HasPrefix(line, `p2p matching report: battle_code="1696492800000" user_id="BBBBBB" result="ggpo" player_count="2" peer_id="1" close_reason="net battle end"`) ||
		strings.ContainsRune(line, 10) || !strings.Contains(line, ` mismatches="0" disconnected="true" disconnected_peer_ids="0"`) || !strings.Contains(line, `ping_median_ms:215`) ||
		!strings.Contains(line, ` total_timesync="17" input_block_count="42" fps_history="60, 59, 60"`) {
		t.Errorf("log line %q", line)
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
	want := &proto.RelayServer{Ip: pb.String("192.168.1.8"), Port: pb.Int32(8203), Token: pb.Uint64(r.SessionToken(1462212142)),
		Ip6: pb.String("2001:db8::8"), Port6: pb.Int32(8204)}
	if got := battleInfo(t, battleInfoNotice(p, info)).Relays; len(got) != 1 || !pb.Equal(got[0], want) {
		t.Fatalf("relays %v, want %v", got, want)
	}
	if len(r.sessions) != 1 {
		t.Fatalf("relay sessions %d, want 1", len(r.sessions))
	}

	// a player without relay support: no relay for the battle
	delete(other, "relay_server")
	if got := battleInfo(t, battleInfoNotice(p, info)).Relays; len(got) != 0 {
		t.Fatalf("relay offered to a battle with a player without relay_server=1: %v", got)
	}
	other["relay_server"] = "1"
	p.PlatformInfo = map[string]string{"emulator": "pcsx2", "udp": "1"}
	if got := battleInfo(t, battleInfoNotice(p, info)).Relays; len(got) != 0 {
		t.Fatalf("relay offered to a client without relay_server=1: %v", got)
	}
}

func TestLiveUsers(t *testing.T) {
	b := model.NewBattle("", 0)
	b.Users = []model.User{{User: db.User{UserID: "AAAAAA", Name: "アムロ\n"}, Bin: capturedUserBinary, Entry: model.EntryTitans},
		{User: db.User{UserID: "BBBBBB", Name: "Bob"}, Entry: model.EntryAeug}}
	var got []string
	for _, u := range liveUsers(b) {
		got = append(got, fmt.Sprintf("%s/%s/%s/%d/%d", u.UserID, u.UserName, u.PilotName, u.Team, u.Pos))
	}
	want := []string{fmt.Sprintf("AAAAAA/アムロ/カミーユ・ビダン/%d/1", model.EntryTitans), fmt.Sprintf("BBBBBB/Bob//%d/2", model.EntryAeug)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("users %v, want %v", got, want)
	}
}
