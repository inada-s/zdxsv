package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func relayPing(peer uint8, sessionID uint32, token uint64, timestamp uint64) []byte {
	p := make([]byte, relayPingSize)
	binary.LittleEndian.PutUint32(p[0:], relayPingMagic)
	p[4] = relayTypePing
	p[5] = peer
	p[6] = 1 // relay_idx, echoed back
	binary.LittleEndian.PutUint32(p[8:], sessionID)
	binary.LittleEndian.PutUint64(p[12:], token)
	binary.LittleEndian.PutUint64(p[20:], timestamp)
	return p
}

func ggpoRelayPacket(from, to, orgType uint8) []byte {
	p := make([]byte, ggpoHeaderSize+4)
	binary.LittleEndian.PutUint16(p[0:], ggpoConstMagic)
	binary.LittleEndian.PutUint16(p[2:], 0x1234)
	binary.LittleEndian.PutUint16(p[4:], 7)
	p[6] = from
	p[7] = ggpoTypeRelay
	binary.LittleEndian.PutUint16(p[8:], ggpoRelayMagic)
	p[10] = to
	p[11] = orgType
	copy(p[ggpoHeaderSize:], []byte{1, 2, 3, 4})
	return p
}

func TestRelaySessionToken(t *testing.T) {
	r := NewRelay()
	a, b := r.SessionToken(100), r.SessionToken(100)
	if a != b {
		t.Fatalf("same session, tokens %x %x", a, b)
	}
	if r.SessionToken(101) == a {
		t.Fatalf("other session got the same token")
	}
}

func TestRelayPing(t *testing.T) {
	r := NewRelay()
	token := r.SessionToken(100)
	a := netip.MustParseAddrPort("1.2.3.4:5000")

	dst, pong := r.handle(relayPing(2, 100, token, 777), a)
	if dst != a || pong[4] != relayTypePong || pong[5] != 2 || pong[6] != 1 || binary.LittleEndian.Uint64(pong[20:]) != 777 {
		t.Fatalf("pong to %v: %x", dst, pong)
	}
	for name, p := range map[string][]byte{
		"wrong token":       relayPing(0, 100, token+1, 1),
		"unknown session":   relayPing(0, 101, token, 1),
		"peer out of range": relayPing(relayMaxPeers, 100, token, 1),
		"short":             relayPing(0, 100, token, 1)[:relayPingSize-1],
	} {
		if dst, _ := r.handle(p, netip.MustParseAddrPort("5.6.7.8:1")); dst.IsValid() {
			t.Errorf("%s: answered", name)
		}
	}
	if len(r.bindings) != 1 {
		t.Errorf("bindings %d, want 1", len(r.bindings))
	}
}

func TestRelayForward(t *testing.T) {
	r := NewRelay()
	token := r.SessionToken(100)
	a := netip.MustParseAddrPort("1.2.3.4:5000")
	b := netip.MustParseAddrPort("[2001:db8::2]:6000")
	r.handle(relayPing(0, 100, token, 1), a)

	// peer 1 not bound yet
	if dst, _ := r.handle(ggpoRelayPacket(0, 1, 3), a); dst.IsValid() {
		t.Fatalf("forwarded to an unbound peer: %v", dst)
	}
	r.handle(relayPing(1, 100, token, 1), b)
	dst, out := r.handle(ggpoRelayPacket(0, 1, 3), a)
	want := ggpoRelayPacket(0, 1, 3)
	want[7] = 3
	want[8], want[9], want[10], want[11] = 0, 0, 0, 0
	if dst != b || !bytes.Equal(out, want) {
		t.Fatalf("forward to %v: %x want %x", dst, out, want)
	}
	for name, c := range map[string]struct {
		p    []byte
		from netip.AddrPort
	}{
		"to self":         {ggpoRelayPacket(0, 0, 3), a},
		"bad type":        {ggpoRelayPacket(0, 1, 99), a},
		"type 0":          {ggpoRelayPacket(0, 1, 0), a},
		"to out of range": {ggpoRelayPacket(0, relayMaxPeers, 3), a},
		"unbound sender":  {ggpoRelayPacket(0, 1, 3), netip.MustParseAddrPort("9.9.9.9:1")},
		"short":           {ggpoRelayPacket(0, 1, 3)[:ggpoHeaderSize-1], a},
	} {
		if dst, _ := r.handle(c.p, c.from); dst.IsValid() {
			t.Errorf("%s: forwarded to %v", name, dst)
		}
	}
}

// A ping over the other family must not move a player whose game packets use this one.
func TestRelayPlayingPathKept(t *testing.T) {
	r := NewRelay()
	token := r.SessionToken(100)
	a4 := netip.MustParseAddrPort("1.2.3.4:5000")
	a6 := netip.MustParseAddrPort("[2001:db8::1]:5000")
	b := netip.MustParseAddrPort("5.6.7.8:6000")
	r.handle(relayPing(0, 100, token, 1), a4)
	r.handle(relayPing(1, 100, token, 1), b)
	r.handle(ggpoRelayPacket(0, 1, 3), a4)
	r.handle(relayPing(0, 100, token, 1), a6)
	if dst, _ := r.handle(ggpoRelayPacket(1, 0, 3), b); dst != a4 {
		t.Fatalf("to %v, want %v", dst, a4)
	}
}

// A NAT rebinding moves a player, even one already playing; the old address is no longer theirs.
func TestRelayRebind(t *testing.T) {
	r := NewRelay()
	token := r.SessionToken(100)
	a := netip.MustParseAddrPort("1.2.3.4:5000")
	a2 := netip.MustParseAddrPort("1.2.3.4:5001")
	b := netip.MustParseAddrPort("5.6.7.8:6000")
	r.handle(relayPing(0, 100, token, 1), a)
	r.handle(relayPing(1, 100, token, 1), b)
	r.handle(ggpoRelayPacket(0, 1, 3), a)

	r.handle(relayPing(0, 100, token, 2), a2)
	if dst, _ := r.handle(ggpoRelayPacket(1, 0, 3), b); dst != a2 {
		t.Fatalf("to %v, want %v", dst, a2)
	}
	if dst, _ := r.handle(ggpoRelayPacket(0, 1, 3), a); dst.IsValid() {
		t.Fatalf("old address still forwards, to %v", dst)
	}
	if dst, _ := r.handle(ggpoRelayPacket(0, 1, 3), a2); dst != b {
		t.Fatalf("new address to %v, want %v", dst, b)
	}
}

// An address another player pings from is taken from the player it was bound to.
func TestRelayAddressTaken(t *testing.T) {
	r := NewRelay()
	token := r.SessionToken(100)
	a := netip.MustParseAddrPort("1.2.3.4:5000")
	b := netip.MustParseAddrPort("5.6.7.8:6000")
	c := netip.MustParseAddrPort("9.9.9.9:7000")
	r.handle(relayPing(0, 100, token, 1), a)
	r.handle(relayPing(1, 100, token, 1), b)
	r.handle(relayPing(2, 100, token, 1), c)
	r.handle(ggpoRelayPacket(0, 1, 3), a)

	r.handle(relayPing(2, 100, token, 2), a)
	if dst, _ := r.handle(ggpoRelayPacket(1, 0, 3), b); dst.IsValid() {
		t.Fatalf("forwarded to the player that lost its address: %v", dst)
	}
	if dst, _ := r.handle(ggpoRelayPacket(1, 2, 3), b); dst != a {
		t.Fatalf("to %v, want %v", dst, a)
	}
	if dst, _ := r.handle(ggpoRelayPacket(2, 1, 3), a); dst != b {
		t.Fatalf("from the taken address to %v, want %v", dst, b)
	}
}

func TestRelayStaleSession(t *testing.T) {
	r := NewRelay()
	now := time.Now()
	r.now = func() time.Time { return now }
	token := r.SessionToken(100)
	r.handle(relayPing(0, 100, token, 1), netip.MustParseAddrPort("1.2.3.4:5000"))
	now = now.Add(relayIdleTimeout + time.Second)
	r.RemoveStaleSessions()
	if len(r.sessions) != 0 || len(r.bindings) != 0 {
		t.Fatalf("sessions %d bindings %d", len(r.sessions), len(r.bindings))
	}
}

// Over real UDP sockets: two players bind with pings, then a GGPO packet goes A -> relay -> B.
func TestRelayServeUDP(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := NewRelay()
	go r.Serve(conn)
	token := r.SessionToken(7)
	relay := conn.LocalAddr().(*net.UDPAddr)

	var players [2]*net.UDPConn
	for i := range players {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		players[i] = c
		c.WriteToUDP(relayPing(uint8(i), 7, token, 42), relay)
		buf := make([]byte, 64)
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := c.ReadFromUDP(buf)
		if err != nil || n != relayPingSize || buf[4] != relayTypePong {
			t.Fatalf("player %d pong: %d %v", i, n, err)
		}
	}
	players[0].WriteToUDP(ggpoRelayPacket(0, 1, 5), relay)
	buf := make([]byte, 64)
	players[1].SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := players[1].ReadFromUDP(buf)
	if err != nil || n != ggpoHeaderSize+4 || buf[7] != 5 || buf[10] != 0 {
		t.Fatalf("relayed: %x %v", buf[:n], err)
	}
}

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
	for _, peer := range []*LbsPeer{p, q} {
		got := battleInfo(t, testBattleInfoNotice(peer, lookup)).Relays
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
	got := battleInfo(t, testBattleInfoNotice(p, lookup)).Relays
	if len(got) != 3 || got[0].GetIp() != "198.51.100.1" || got[1].GetIp() != "198.51.100.2" || got[2].GetIp() != "192.168.1.8" {
		t.Fatalf("relays %v, want asia-northeast1, asia-northeast2, the lobby's", got)
	}
	if got[2].GetToken() != LobbyRelay.SessionToken(1462212142) {
		t.Errorf("lobby relay token %d", got[2].GetToken())
	}

	// no lobby relay: the regional ones alone
	LobbyRelay = nil
	if got := battleInfo(t, testBattleInfoNotice(p, lookup)).Relays; len(got) != 2 {
		t.Fatalf("relays without a lobby relay %v, want 2", got)
	}

	// a player without relay support: none
	delete(infoA, "relay_server")
	if got := battleInfo(t, testBattleInfoNotice(p, lookup)).Relays; len(got) != 0 {
		t.Fatalf("relays offered to a battle with a player without relay_server=1: %v", got)
	}
}

func registeredRelay(addr string) *RegionalRelay {
	regionalRelays.Lock()
	defer regionalRelays.Unlock()
	return regionalRelays.byAddr[addr]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func startRegionalRelay(t *testing.T, ctx context.Context, lobbyAddr, secret string) (*net.UDPAddr, chan error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	addr := conn.LocalAddr().(*net.UDPAddr)
	done := make(chan error, 1)
	go func() {
		done <- RunRegionalRelay(ctx, conn, lobbyAddr, RelayStatus{Region: "asia-northeast1", PublicAddr: addr.String(), Secret: secret})
	}()
	return addr, done
}

func TestRegionalRelayRegistersAndRelays(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go ServeRelayControl(l, "s3cret")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, wrongDone := startRegionalRelay(t, ctx, l.Addr().String(), "wrong")
	relay, done := startRegionalRelay(t, ctx, l.Addr().String(), "s3cret")
	waitFor(t, "registration", func() bool { return registeredRelay(relay.String()) != nil })

	infoA := map[string]string{"asia-northeast1": "20"}
	infoB := map[string]string{"asia-northeast1": "30"}
	info := map[string]map[string]string{"AAAAAA": infoA, "BBBBBB": infoB}
	relays := battleRelays([]string{"AAAAAA", "BBBBBB"}, func(id string) map[string]string { return info[id] })
	if len(relays) != 1 || relays[0].PublicAddr != relay.String() || relays[0].Region != "asia-northeast1" {
		t.Fatalf("battle relays %+v, want only %s", relays, relay)
	}
	token := relays[0].Sessions.SessionToken(9)
	if relays[0].Sessions.SessionToken(9) != token {
		t.Fatalf("second ask for session 9 got another token")
	}

	var players [2]*net.UDPConn
	for i := range players {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		players[i] = c
		buf := make([]byte, 64)
		waitFor(t, "pong with the pushed token", func() bool {
			c.WriteToUDP(relayPing(uint8(i), 9, token, 42), relay)
			c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := c.ReadFromUDP(buf)
			return err == nil && n == relayPingSize && buf[4] == relayTypePong
		})
	}
	players[0].WriteToUDP(ggpoRelayPacket(0, 1, 5), relay)
	buf := make([]byte, 64)
	players[1].SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := players[1].ReadFromUDP(buf)
	if err != nil || n != ggpoHeaderSize+4 || buf[7] != 5 || buf[10] != 0 {
		t.Fatalf("relayed: %x %v", buf[:n], err)
	}

	cancel()
	<-done
	<-wrongDone
	waitFor(t, "unregistration", func() bool { return registeredRelay(relay.String()) == nil })
}

func TestRegionalRelayShutdown(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, done := startRegionalRelay(t, ctx, l.Addr().String(), "s3cret")
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte(`{"shutdown":true}` + "\n"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("relay stopped with %v, want a shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay still running after the shutdown")
	}
}

func TestTestRelaySession(t *testing.T) {
	now := time.Now()
	r, maintain := newTestRelay(9, 0xABC)
	r.now = func() time.Time { return now }
	a := netip.MustParseAddrPort("1.2.3.4:5000")
	if dst, _ := r.handle(relayPing(0, 9, 0xABC, 1), a); dst != a {
		t.Fatal("ping with the test token not answered")
	}
	if dst, _ := r.handle(relayPing(0, 9, 0xABD, 1), a); dst.IsValid() {
		t.Error("ping with another token answered")
	}

	now = now.Add(relayIdleTimeout + time.Second)
	maintain()
	s, ok := r.sessions[9]
	if !ok || !s.created.Equal(now) {
		t.Fatalf("stale test session not registered again: %+v", s)
	}
	if dst, _ := r.handle(relayPing(1, 9, 0xABC, 2), a); dst != a {
		t.Error("ping not answered after the session was registered again")
	}
}

func TestRelayServeMaintained(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	var calls, late atomic.Int32
	returned := make(chan struct{})
	go func() {
		NewRelay().serveMaintained(conn, 5*time.Millisecond, func() {
			select {
			case <-returned:
				late.Add(1)
			default:
			}
			calls.Add(1)
		})
		close(returned)
	}()
	for end := time.Now().Add(5 * time.Second); calls.Load() < 2; time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("maintain called %d times in 5 s", calls.Load())
		}
	}

	conn.Close()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("serveMaintained did not return after the conn closed")
	}
	if n := late.Load(); n != 0 {
		t.Errorf("maintain called %d times after serveMaintained returned", n)
	}
}

// relayStarter starts a real regional relay loop for each StartRelay.
type relayStarter struct {
	t         *testing.T
	ctx       context.Context
	lobbyAddr string
	mtx       sync.Mutex
	started   []string
	addrs     map[string]string
	done      map[string]chan error
}

func (s *relayStarter) StartRelay(region string) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		s.t.Error(err)
		return
	}
	s.t.Cleanup(func() { conn.Close() })
	done := make(chan error, 1)
	s.mtx.Lock()
	s.started = append(s.started, region)
	s.addrs[region] = conn.LocalAddr().String()
	s.done[region] = done
	s.mtx.Unlock()
	go func() {
		done <- RunRegionalRelay(s.ctx, conn, s.lobbyAddr, RelayStatus{Region: region, PublicAddr: conn.LocalAddr().String(), Secret: "s3cret"})
	}()
}

func (s *relayStarter) startedRegions() []string {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	return append([]string(nil), s.started...)
}

func addRelayUser(a *Lbs, id string, info map[string]string) {
	p := &LbsPeer{PlatformInfo: info}
	p.UserID = id
	a.users[id] = p
}

func TestRelayProvisioning(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go ServeRelayControl(l, "s3cret")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	starter := &relayStarter{t: t, ctx: ctx, lobbyAddr: l.Addr().String(), addrs: map[string]string{}, done: map[string]chan error{}}

	a := NewLbs()
	a.SetRelayProvisioning([]string{"asia-northeast1", "us-west2"}, starter)
	t0 := time.Now()

	tokyo := map[string]string{"relay_server": "1", "asia-northeast1": "10", "us-west2": "120"}
	osaka := map[string]string{"relay_server": "1", "asia-northeast2": "9", "us-west2": "130"}
	la := map[string]string{"relay_server": "1", "asia-northeast1": "110", "us-west2": "15"}
	laNoRelay := map[string]string{"asia-northeast1": "110", "us-west2": "15"}
	addRelayUser(a, "AAAAAA", tokyo)
	addRelayUser(a, "BBBBBB", osaka)
	addRelayUser(a, "CCCCCC", tokyo)
	addRelayUser(a, "DDDDDD", laNoRelay)
	a.updateRelays(t0)
	if s := starter.startedRegions(); len(s) != 0 {
		t.Fatalf("3 relay users: started %v", s)
	}
	addRelayUser(a, "DDDDDD", tokyo)
	a.updateRelays(t0)
	if s := starter.startedRegions(); len(s) != 0 {
		t.Fatalf("4 relay users of one group: started %v", s)
	}

	addRelayUser(a, "DDDDDD", la)
	a.updateRelays(t0)
	if s := starter.startedRegions(); len(s) != 2 || s[0] != "asia-northeast1" || s[1] != "us-west2" {
		t.Fatalf("4 relay users of two groups: started %v, want both regions", s)
	}
	tokyoAddr, laAddr := starter.addrs["asia-northeast1"], starter.addrs["us-west2"]
	t.Cleanup(func() { UnregisterRelay(tokyoAddr); UnregisterRelay(laAddr) })
	waitFor(t, "both relays registered", func() bool { return registeredRelay(tokyoAddr) != nil && registeredRelay(laAddr) != nil })

	a.updateRelays(t0.Add(relayStartRetry))
	if s := starter.startedRegions(); len(s) != 2 {
		t.Fatalf("registered relays started again: %v", s)
	}
	relays := battleRelays([]string{"AAAAAA", "CCCCCC"}, func(id string) map[string]string { return a.users[id].PlatformInfo })
	if len(relays) != 2 || relays[0].PublicAddr != tokyoAddr {
		t.Fatalf("battle relays of two Tokyo players: %+v, want Tokyo first", relays)
	}

	delete(a.users, "DDDDDD")
	lastNeeded := t0.Add(relayStartRetry)
	a.updateRelays(lastNeeded.Add(relayIdleShutdown - time.Second))
	regionalRelays.Lock()
	idle := regionalRelays.byAddr[laAddr]
	regionalRelays.byAddr[tokyoAddr].activeSessions = 1
	regionalRelays.Unlock()
	if idle == nil || idle.shutdown == nil {
		t.Fatal("relay shut down before relayIdleShutdown")
	}
	a.updateRelays(lastNeeded.Add(relayIdleShutdown))
	select {
	case err := <-starter.done["us-west2"]:
		if err != nil {
			t.Fatalf("idle relay stopped with %v, want a shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle relay still running after relayIdleShutdown")
	}
	select {
	case err := <-starter.done["asia-northeast1"]:
		t.Fatalf("relay with a session stopped: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	waitFor(t, "idle relay unregistered", func() bool { return registeredRelay(laAddr) == nil })
}
