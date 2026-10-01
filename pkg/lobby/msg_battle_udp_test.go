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
	n := battleInfoNotice(newUDPTestPeer(emu, false))
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
		if battleInfoNotice(p) != nil {
			t.Errorf("%s: notice sent", name)
		}
	}
}
