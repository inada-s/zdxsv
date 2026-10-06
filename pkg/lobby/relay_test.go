package lobby

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
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
