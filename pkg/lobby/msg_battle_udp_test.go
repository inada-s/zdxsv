package lobby

import (
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"zdxsv/pkg/db"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
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

func TestBattleInfoNotice(t *testing.T) {
	emu := map[string]string{"emulator": "pcsx2", "cpu": "x86/64", "udp": "1"}
	n := battleInfoNotice(newUDPTestPeer(emu, false), nil)
	if n == nil {
		t.Fatal("no notice for udp emulator")
	}
	if n.Direction != ServerToClient || n.Category != CategoryCustom || n.Command != 0x9951 {
		t.Errorf("header %x %x %x", n.Direction, n.Category, n.Command)
	}
	want := "session_id=SBBBBBB\nuser_id=BBBBBB\nbattle_code=\nbattle_server=192.168.1.8:8210\nusers=AAAAAA,BBBBBB\n"
	if string(n.Body) != want {
		t.Errorf("body %q want %q", n.Body, want)
	}
	if got := model.ParsePlatformInfo(string(n.Body)); got["users"] != "AAAAAA,BBBBBB" {
		t.Errorf("not key=value lines: %v", got)
	}

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

func TestBattleInfoNoticeP2P(t *testing.T) {
	emu := map[string]string{"emulator": "pcsx2", "udp": "1"}
	p := newUDPTestPeer(emu, false)
	p.Battle.Users = append(p.Battle.Users,
		model.User{User: db.User{UserID: "CCCCCC", SessionID: "SCCCCCC"}},
		model.User{User: db.User{UserID: "DDDDDD", SessionID: "SDDDDDD"}})
	infos := map[string]map[string]string{
		// every address (IPv4 public, local, IPv6)
		"AAAAAA": {"udp": "1", "udp_addr": "203.0.113.5:40001", "udp_local": "192.168.1.20:40001", "udp_addr6": "[2001:db8::5]:40001", "ggpo": "7001"},
		// self: never listed
		"BBBBBB": {"udp": "1", "udp_addr": "203.0.113.6:40002"},
		// local IPv4 only (no STUN answer), and IPv6
		"CCCCCC": {"udp": "1", "udp_local": "192.168.1.21:40003", "udp_addr6": "[2001:db8::21]:40003", "ggpo": "70000"},
		// not a bridge: no p2p line even with an address
		"DDDDDD": {"udp_addr": "203.0.113.7:40004"},
	}
	p.Battle.BattleCode = "1696492800000"
	// names as login stores them (ReadEncryptedString already decoded Shift-JIS):
	// "アムロ", a control byte (dropped), none (no line)
	p.Battle.Users[0].Name = "アムロ"
	p.Battle.Users[1].Name = "Bob\n"
	// a pilot name as login stores the game's user binary (rig capture, NULs at both ends trimmed)
	p.Battle.Users[0].Bin = capturedUserBinary
	n := battleInfoNotice(p, func(id string) map[string]string { return infos[id] })
	// ggpo_session = FNV-1 32 of "1696492800000" (independent python computation)
	want := "session_id=SBBBBBB\nuser_id=BBBBBB\nbattle_code=1696492800000\nbattle_server=192.168.1.8:8210\nusers=AAAAAA,BBBBBB,CCCCCC,DDDDDD\n" +
		"name_AAAAAA=アムロ\npilot_AAAAAA=カミーユ・ビダン\nname_BBBBBB=Bob\n" +
		"p2p_AAAAAA=203.0.113.5:40001,192.168.1.20:40001,[2001:db8::5]:40001\nggpo_AAAAAA=7001\n" +
		"p2p_CCCCCC=192.168.1.21:40003,[2001:db8::21]:40003\n" +
		"ggpo_session=1462212142\nggpo_ping_ms=7500\n"
	if n == nil {
		t.Fatal("no notice")
	}
	if string(n.Body) != want {
		t.Fatalf("body %q want %q", n.Body, want)
	}
}

func TestBattleInfoNoticeSync(t *testing.T) {
	emu := map[string]string{"emulator": "pcsx2", "udp": "1", "sync": "0123456789abcdef"}
	p := newUDPTestPeer(emu, false)
	p.Battle.Users = append(p.Battle.Users, model.User{User: db.User{UserID: "CCCCCC", SessionID: "SCCCCCC"}})
	infos := map[string]map[string]string{
		"AAAAAA": {"udp": "1", "sync": "fedcba9876543210"},
		// not 16 lowercase hex digits: no line
		"CCCCCC": {"udp": "1", "sync": "FEDCBA9876543210"},
	}
	n := battleInfoNotice(p, func(id string) map[string]string { return infos[id] })
	want := "session_id=SBBBBBB\nuser_id=BBBBBB\nbattle_code=\nbattle_server=192.168.1.8:8210\nusers=AAAAAA,BBBBBB,CCCCCC\n" +
		"sync_AAAAAA=fedcba9876543210\nsync_BBBBBB=0123456789abcdef\n"
	if n == nil || string(n.Body) != want {
		t.Fatalf("body %q want %q", n.Body, want)
	}
}

// capturedUserBinary is a SetUserBinary body from a pcsx2 rig after ReadEncryptedString:
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

func TestP2PMatchingReportLine(t *testing.T) {
	body := "battle_code=1696492800000\nuser_id=BBBBBB\nresult=ggpo\ndelay=2\nrtt_0=12\nframes=21563\nclose=net battle end\n"
	got := p2pMatchingReportLine(model.ParsePlatformInfo(body))
	want := `p2p matching report: battle_code="1696492800000" user_id="BBBBBB" result="ggpo" close="net battle end" delay="2" frames="21563" rtt_0="12"`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestBattleInfoNoticeRelay(t *testing.T) {
	r := NewRelay()
	LobbyRelay, RelayPublicAddr, RelayPublicAddr6 = r, "192.168.1.8:8203", "[2001:db8::8]:8203"
	defer func() { LobbyRelay, RelayPublicAddr, RelayPublicAddr6 = nil, "", "" }()
	emu := map[string]string{"emulator": "pcsx2", "udp": "1", "relay_server": "1"}
	p := newUDPTestPeer(emu, false)
	p.Battle.BattleCode = "1696492800000"
	other := map[string]string{"udp": "1", "udp_addr": "203.0.113.5:40001", "ggpo": "7001", "relay_server": "1"}
	info := func(id string) map[string]string { return other }

	n := battleInfoNotice(p, info)
	// session 1462212142 = ggpo_session of this battle code (TestBattleInfoNoticeP2P)
	want := "ggpo_session=1462212142\nggpo_ping_ms=7500\nrelay_0=" +
		strconv.FormatUint(r.SessionToken(1462212142), 16) + ",192.168.1.8:8203,[2001:db8::8]:8203\n"
	if n == nil || !strings.HasSuffix(string(n.Body), want) {
		t.Fatalf("body %q, want suffix %q", n.Body, want)
	}
	if len(r.sessions) != 1 {
		t.Fatalf("relay sessions %d, want 1", len(r.sessions))
	}

	// a player without relay support: no relay for the battle
	delete(other, "relay_server")
	if n := battleInfoNotice(p, info); strings.Contains(string(n.Body), "relay_") {
		t.Fatalf("relay offered to a battle with a player without relay_server=1: %q", n.Body)
	}
	other["relay_server"] = "1"
	p.PlatformInfo = map[string]string{"emulator": "pcsx2", "udp": "1"}
	if n := battleInfoNotice(p, info); strings.Contains(string(n.Body), "relay_") {
		t.Fatalf("relay offered to a client without relay_server=1: %q", n.Body)
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
