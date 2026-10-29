package lobby

import (
	"testing"
)

func TestBattleInfoNoticeRegionalRelays(t *testing.T) {
	LobbyRelay, RelayPublicAddr = NewRelay(), "192.168.1.8:8204"
	defer func() { LobbyRelay, RelayPublicAddr = nil, "" }()
	relays := map[string]*Relay{}
	for _, r := range []struct{ region, addr, addr6 string }{
		{"asia-northeast1", "198.51.100.1:8204", "[2001:db8::1]:8204"},
		{"asia-northeast2", "198.51.100.2:8204", ""},
		{"asia-east1", "198.51.100.3:8204", ""},
		{"us-west1", "198.51.100.4:8204", ""},
	} {
		relays[r.addr] = NewRelay()
		RegisterRelay(&RegionalRelay{Region: r.region, PublicAddr: r.addr, PublicAddr6: r.addr6, Sessions: relays[r.addr]})
		defer UnregisterRelay(r.addr)
	}
	infoA := map[string]string{"emulator": "pcsx2", "udp": "1", "udp_addr": "203.0.113.5:40001", "ggpo": "7001", "relay_server": "1",
		"asia-northeast1": "20", "asia-northeast2": "40", "asia-east1": "60", "us-west1": "100"}
	infoB := map[string]string{"emulator": "pcsx2", "udp": "1", "udp_addr": "203.0.113.6:40001", "ggpo": "7002", "relay_server": "1",
		"asia-northeast1": "30", "asia-northeast2": "80", "us-west1": "100"}
	p := newUDPTestPeer(infoB, false)
	p.Battle.BattleCode = "1696492800000"
	p.Battle.GgpoSession = 1462212142
	q := newUDPTestPeer(infoA, false)
	q.UserID, q.Battle = "AAAAAA", p.Battle
	info := map[string]map[string]string{"AAAAAA": infoA, "BBBBBB": infoB}
	lookup := func(id string) map[string]string { return info[id] }

	// relayed RTT 50, 120, 200, then asia-east1 (B has no latency to it); the lobby's relay
	// is the 5th and dropped
	want := []string{"198.51.100.1", "198.51.100.2", "198.51.100.4", "198.51.100.3"}
	for _, peer := range []*AppPeer{p, q} {
		got := battleInfo(t, battleInfoNotice(peer, lookup)).Relays
		if len(got) != len(want) {
			t.Fatalf("%s: relays %v, want %v", peer.UserID, got, want)
		}
		for i, r := range got {
			if r.GetIp() != want[i] || r.GetPort() != 8204 {
				t.Errorf("%s: relay_%d %v, want %s:8204", peer.UserID, i, r, want[i])
			}
			if tok := relays[want[i]+":8204"].SessionToken(1462212142); r.GetToken() != tok {
				t.Errorf("%s: relay_%d token %d, want the relay's session token %d", peer.UserID, i, r.GetToken(), tok)
			}
		}
		if got[0].GetIp6() != "2001:db8::1" || got[1].GetIp6() != "" {
			t.Errorf("%s: ip6 %q %q", peer.UserID, got[0].GetIp6(), got[1].GetIp6())
		}
	}

	// fewer regional relays: the lobby's own relay last
	UnregisterRelay("198.51.100.3:8204")
	UnregisterRelay("198.51.100.4:8204")
	got := battleInfo(t, battleInfoNotice(p, lookup)).Relays
	if len(got) != 3 || got[0].GetIp() != "198.51.100.1" || got[1].GetIp() != "198.51.100.2" || got[2].GetIp() != "192.168.1.8" {
		t.Fatalf("relays %v, want asia-northeast1, asia-northeast2, the lobby's", got)
	}
	if got[2].GetToken() != LobbyRelay.SessionToken(1462212142) {
		t.Errorf("lobby relay token %d", got[2].GetToken())
	}

	// no lobby relay: the regional ones alone
	LobbyRelay = nil
	if got := battleInfo(t, battleInfoNotice(p, lookup)).Relays; len(got) != 2 {
		t.Fatalf("relays without a lobby relay %v, want 2", got)
	}

	// a player without relay support: none
	delete(infoA, "relay_server")
	if got := battleInfo(t, battleInfoNotice(p, lookup)).Relays; len(got) != 0 {
		t.Fatalf("relays offered to a battle with a player without relay_server=1: %v", got)
	}
}
