package lobby

import (
	"net"
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
	want := "session_id=SBBBBBB\nuser_id=BBBBBB\nbattle_server=192.168.1.8:8210\nusers=AAAAAA,BBBBBB\n"
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
		// both addresses
		"AAAAAA": {"udp": "1", "udp_addr": "203.0.113.5:40001", "udp_local": "192.168.1.20:40001"},
		// self: never listed
		"BBBBBB": {"udp": "1", "udp_addr": "203.0.113.6:40002"},
		// local only (no STUN answer)
		"CCCCCC": {"udp": "1", "udp_local": "192.168.1.21:40003"},
		// not a bridge: no p2p line even with an address
		"DDDDDD": {"udp_addr": "203.0.113.7:40004"},
	}
	n := battleInfoNotice(p, func(id string) map[string]string { return infos[id] })
	want := "session_id=SBBBBBB\nuser_id=BBBBBB\nbattle_server=192.168.1.8:8210\nusers=AAAAAA,BBBBBB,CCCCCC,DDDDDD\n" +
		"p2p_AAAAAA=203.0.113.5:40001,192.168.1.20:40001\np2p_CCCCCC=192.168.1.21:40003\n"
	if n == nil {
		t.Fatal("no notice")
	}
	if string(n.Body) != want {
		t.Fatalf("body %q want %q", n.Body, want)
	}
}
