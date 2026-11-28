package main

import (
	"bytes"
	"encoding/json"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// startUDPServer serves on a local port with U1 (12345678) and U2 (87654321) waiting for battle.
func startUDPServer(t *testing.T) (*McsUDPServer, *net.UDPAddr) {
	t.Helper()
	mcs := NewMcs()
	mcs.AddBattle([]McsUser{
		{UserID: "U1", SessionID: "12345678"}, {UserID: "U2", SessionID: "87654321"},
	})
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s := NewUDPServer(mcs)
	done := make(chan struct{})
	go func() {
		s.Serve(conn)
		close(done)
	}()
	t.Cleanup(func() {
		conn.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after the conn was closed")
		}
	})
	return s, conn.LocalAddr().(*net.UDPAddr)
}

func (s *McsUDPServer) peerCount() int {
	s.Lock()
	defer s.Unlock()
	return len(s.peers)
}

func waitPeerCount(t *testing.T, s *McsUDPServer, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for s.peerCount() != n {
		if time.Now().After(deadline) {
			t.Fatalf("peer count = %d, want %d", s.peerCount(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// udpClient sends like the game client: every packet carries all its messages so far.
type udpClient struct {
	t      *testing.T
	conn   *net.UDPConn
	server *net.UDPAddr
	userID string
	sent   []McsMessage
	recv   map[string]uint32
	ack    uint32
}

func newUDPClient(t *testing.T, server *net.UDPAddr, userID string) *udpClient {
	t.Helper()
	return newUDPClientAt(t, server, userID, net.IPv4(127, 0, 0, 1))
}

func newUDPClientAt(t *testing.T, server *net.UDPAddr, userID string, ip net.IP) *udpClient {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &udpClient{t: t, conn: conn, server: server, userID: userID, recv: map[string]uint32{}}
}

func (c *udpClient) write(data []byte) {
	c.t.Helper()
	if _, err := c.conn.WriteToUDP(data, c.server); err != nil {
		c.t.Fatal(err)
	}
}

func (c *udpClient) read(timeout time.Duration) *Packet {
	c.t.Helper()
	buf := make([]byte, 4096)
	c.conn.SetReadDeadline(time.Now().Add(timeout))
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil
	}
	pkt, err := ParsePacket(buf[:n])
	if err != nil {
		c.t.Fatal(err)
	}
	if pkt.Kind == KindBattle {
		c.ack = pkt.Seq
	}
	return &pkt
}

// readAll returns the packets that arrive within d.
func (c *udpClient) readAll(d time.Duration) []*Packet {
	c.t.Helper()
	var pkts []*Packet
	deadline := time.Now().Add(d)
	for {
		pkt := c.read(time.Until(deadline))
		if pkt == nil {
			return pkts
		}
		pkts = append(pkts, pkt)
	}
}

// hello greets like zproxy, which then sends battle packets at once and acks
// the server's seq. The server makes the peer on the first send after it.
func (c *udpClient) hello(sessionID string) bool {
	c.t.Helper()
	c.write(AppendHello(nil, sessionID))
	pkt := c.read(2 * time.Second)
	if pkt == nil || pkt.Kind != KindHelloAnswer || pkt.SessionID != sessionID {
		c.t.Fatalf("hello reply = %v", pkt)
	}
	if !pkt.OK {
		return false
	}
	c.write(AppendBattle(nil, uint32(len(c.sent)), c.ack, c.sent))
	if pkt := c.read(2 * time.Second); pkt == nil || pkt.Kind != KindBattle {
		c.t.Fatalf("battle reply = %v", pkt)
	}
	return true
}

func (c *udpClient) send(bodies ...[]byte) {
	c.t.Helper()
	for _, b := range bodies {
		c.sent = append(c.sent, McsMessage{UserID: c.userID, Seq: uint32(len(c.sent) + 1), Body: b})
	}
	c.write(AppendBattle(nil, uint32(len(c.sent)), c.ack, c.sent))
}

// receive collects relayed message bodies not seen before, until it has n or times out.
func (c *udpClient) receive(n int, timeout time.Duration) [][]byte {
	c.t.Helper()
	var got [][]byte
	deadline := time.Now().Add(timeout)
	for len(got) < n && time.Now().Before(deadline) {
		pkt := c.read(time.Until(deadline))
		if pkt == nil {
			break
		}
		for _, m := range pkt.Messages {
			if m.Seq > c.recv[m.UserID] {
				c.recv[m.UserID] = m.Seq
				got = append(got, append([]byte(m.UserID+":"), m.Body...))
			}
		}
	}
	return got
}

// udpJoin is the first battle message: the encoded session id at bytes 12..22.
func udpJoin(encoded string) []byte {
	return append(make([]byte, 12), encoded...)
}

func wantBodies(t *testing.T, got [][]byte, want ...string) {
	t.Helper()
	var w [][]byte
	for _, s := range want {
		w = append(w, []byte(s))
	}
	if len(got) != len(w) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range w {
		if !bytes.Equal(got[i], w[i]) {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestUDPHello(t *testing.T) {
	s, addr := startUDPServer(t)
	c := newUDPClient(t, addr, "U1")
	if c.hello("99999999") {
		t.Error("unknown session accepted")
	}
	if s.peerCount() != 0 {
		t.Errorf("peer created for an unknown session")
	}
	// The client repeats its hello: one peer per address.
	for i := 0; i < 3; i++ {
		c.write(AppendHello(nil, "12345678"))
		if pkt := c.read(2 * time.Second); pkt == nil || !pkt.OK {
			t.Fatalf("hello reply = %v", pkt)
		}
	}
	if !c.hello("12345678") {
		t.Fatal("waiting session rejected")
	}
	if s.peerCount() != 0 {
		t.Errorf("peer created before the client acked")
	}
	c.send()
	waitPeerCount(t, s, 1)
}

func TestUDPPeerNeedsServerSeq(t *testing.T) {
	s, addr := startUDPServer(t)
	c := newUDPClient(t, addr, "U1")
	c.write(AppendHello(nil, "12345678"))
	if pkts := c.readAll(300 * time.Millisecond); len(pkts) != 1 || pkts[0].Kind != KindHelloAnswer {
		t.Fatalf("hello: got %v, want one answer", pkts)
	}

	for _, ack := range []uint32{0, 1, 12345678} {
		c.write(AppendBattle(nil, 0, ack, nil))
		pkts := c.readAll(300 * time.Millisecond)
		if len(pkts) != 1 || pkts[0].Kind != KindBattle || len(pkts[0].Messages) != 0 {
			t.Fatalf("ack %d: got %v, want one empty battle packet", ack, pkts)
		}
		if s.peerCount() != 0 {
			t.Fatalf("ack %d: peer created", ack)
		}
	}
	seq := c.ack

	other := newUDPClient(t, addr, "U1")
	other.write(AppendBattle(nil, 0, seq, nil))
	if pkts := other.readAll(300 * time.Millisecond); len(pkts) != 0 {
		t.Fatalf("other address: got %v, want nothing", pkts)
	}

	c.send()
	waitPeerCount(t, s, 1)
}

func TestUDPHelloAfterWaitExpires(t *testing.T) {
	s, addr := startUDPServer(t)
	s.mcs.mtx.Lock()
	s.mcs.now = func() time.Time { return time.Now().Add(mcsWaitingTTL + time.Second) }
	s.mcs.mtx.Unlock()
	c := newUDPClient(t, addr, "U1")
	if c.hello("12345678") {
		t.Error("expired session accepted")
	}
}

func TestUDPRelay(t *testing.T) {
	s, addr := startUDPServer(t)
	c1 := newUDPClient(t, addr, "U1")
	c2 := newUDPClient(t, addr, "U2")
	if !c1.hello("12345678") || !c2.hello("87654321") {
		t.Fatal("hello rejected")
	}
	// "0123505679" parses to "12345678": see ParseSessionID
	c1.send(udpJoin("0123505679"))
	c2.send(udpJoin("0876604322"))
	waitJoined(t, s.mcs, 2)

	// Resent messages (no ack yet) reach the other peer once.
	c1.send([]byte("a1"))
	c1.send([]byte("a2"), []byte("a3"))
	wantBodies(t, c2.receive(3, 2*time.Second), "U1:a1", "U1:a2", "U1:a3")
	c2.send([]byte("b1"))
	wantBodies(t, c1.receive(1, 2*time.Second), "U2:b1")

	// Fin data ends the sender's peer; its later data is dropped.
	c1.send([]byte{4, 240, 0, 0})
	waitPeerCount(t, s, 1)
	c1.send([]byte("a5"))
	wantBodies(t, c2.receive(1, 300*time.Millisecond))

	c2.send([]byte("b2"))
	wantBodies(t, c1.receive(1, 300*time.Millisecond))
}

func TestUDPBadJoinClosesPeer(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"short", "0123505679"},
		{"not a number", string(udpJoin("01235x5679"))},
		{"not waiting", string(udpJoin("0999999999"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, addr := startUDPServer(t)
			c := newUDPClient(t, addr, "U1")
			if !c.hello("12345678") {
				t.Fatal("hello rejected")
			}
			c.send()
			waitPeerCount(t, s, 1)
			c.send([]byte(tc.body))
			waitPeerCount(t, s, 0)
		})
	}
}

func TestWireRoundTrip(t *testing.T) {
	msgs := []McsMessage{{UserID: "U1", Seq: 7, Body: []byte{1, 2, 3}}, {UserID: "USER02", Seq: 1 << 31, Body: []byte{}}}
	for _, c := range []struct {
		b    []byte
		want Packet
	}{
		{AppendHello(nil, "12345678"), Packet{Kind: KindHello, SessionID: "12345678"}},
		{AppendHelloAnswer(nil, "12345678", true), Packet{Kind: KindHelloAnswer, SessionID: "12345678", OK: true}},
		{AppendHelloAnswer(nil, "", false), Packet{Kind: KindHelloAnswer}},
		{AppendBattle(nil, 5, 4, nil), Packet{Kind: KindBattle, Seq: 5, Ack: 4}},
		{AppendBattle(nil, 1, 0, msgs), Packet{Kind: KindBattle, Seq: 1, Messages: msgs}},
	} {
		got, err := ParsePacket(c.b)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParsePacket(% x) = %+v, %v; want %+v", c.b, got, err, c.want)
		}
	}
}

func TestWireBad(t *testing.T) {
	full := AppendBattle(nil, 1, 0, []McsMessage{{UserID: "U1", Seq: 1, Body: []byte{9, 9}}})
	for i := 3; i < len(full); i++ {
		if i == 10 {
			continue // no messages is valid
		}
		if _, err := ParsePacket(full[:i]); err == nil {
			t.Errorf("ParsePacket(% x) accepted a cut packet", full[:i])
		}
	}
	for _, b := range [][]byte{nil, {0}, {0, 9}, {0, KindHelloAnswer}} {
		if _, err := ParsePacket(b); err == nil {
			t.Errorf("ParsePacket(% x) = nil error", b)
		}
	}
}

func TestWireNotStun(t *testing.T) {
	ping, err := json.Marshal(StunPacket{Type: StunPing, Timestamp: 1})
	if err != nil {
		t.Fatal(err)
	}
	if IsWirePacket(ping) {
		t.Errorf("Ping % x taken for a battle packet", ping)
	}
}

func TestUDPAckOutOfWindowIgnored(t *testing.T) {
	s, addr := startUDPServer(t)
	c1 := newUDPClient(t, addr, "U1")
	c2 := newUDPClient(t, addr, "U2")
	if !c1.hello("12345678") || !c2.hello("87654321") {
		t.Fatal("hello rejected")
	}
	c1.send(udpJoin("0123505679"))
	c2.send(udpJoin("0876604322"))
	waitJoined(t, s.mcs, 2)
	c2.send([]byte("b1"))
	wantBodies(t, c1.receive(1, 2*time.Second), "U2:b1")

	c1.write(AppendBattle(nil, uint32(len(c1.sent)), c1.ack+1000, c1.sent))
	c2.send([]byte("b2"))
	buf := make([]byte, 64<<10)
	deadline := time.Now().Add(2 * time.Second)
	for {
		c1.conn.SetReadDeadline(deadline)
		n, err := c1.conn.Read(buf)
		if err != nil {
			t.Fatal("b2 not received:", err)
		}
		pkt, err := ParsePacket(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		if len(pkt.Messages) > 2 {
			t.Fatalf("%d messages after an ack past the sent seq, want at most 2", len(pkt.Messages))
		}
		if len(pkt.Messages) == 2 && string(pkt.Messages[1].Body) == "b2" {
			return
		}
	}
}

// served waits until the server answers a hello from another address, so it
// has read every packet sent before; then whatever it sent c is already queued.
func served(t *testing.T, addr *net.UDPAddr, ip net.IP) {
	t.Helper()
	m := newUDPClientAt(t, addr, "M", ip)
	m.write(AppendHello(nil, "99999999"))
	if pkt := m.read(2 * time.Second); pkt == nil || pkt.Kind != KindHelloAnswer {
		t.Fatalf("marker hello reply = %v", pkt)
	}
}

func TestUDPHelloLimit(t *testing.T) {
	s, addr := startUDPServer(t)
	const perIP = 5
	s.hellos.mu.Lock()
	s.hellos.perIP = perIP
	s.hellos.mu.Unlock()
	c := newUDPClient(t, addr, "U1")
	for i := 0; i < perIP+3; i++ {
		c.write(AppendHello(nil, "99999999"))
	}
	for i := 0; i < perIP; i++ {
		if pkt := c.read(2 * time.Second); pkt == nil || pkt.Kind != KindHelloAnswer {
			t.Fatalf("hello answer %d = %v", i+1, pkt)
		}
	}
	served(t, addr, net.IPv4(127, 0, 0, 2))
	if pkt := c.read(time.Millisecond); pkt != nil {
		t.Errorf("hello answered past the limit of %d: %v", perIP, pkt)
	}
}

func TestUDPHelloAnsweredWhenLimiterFull(t *testing.T) {
	s, addr := startUDPServer(t)
	s.hellos.mu.Lock()
	s.hellos.start = time.Now()
	s.hellos.count = make(map[string]int, limiterTrackedIPsMax)
	for i := 0; i < limiterTrackedIPsMax; i++ {
		s.hellos.count[strconv.Itoa(i)] = 1
	}
	s.hellos.mu.Unlock()
	c := newUDPClient(t, addr, "U1")
	if !c.hello("12345678") {
		t.Fatal("hello rejected")
	}
}

func TestUDPPeersPerUser(t *testing.T) {
	s, addr := startUDPServer(t)
	for i := 1; i <= udpPeersPerUser+1; i++ {
		c := newUDPClient(t, addr, "U1")
		if !c.hello("12345678") {
			t.Fatal("hello rejected")
		}
		c.send()
		if i <= udpPeersPerUser {
			waitPeerCount(t, s, i)
			continue
		}
		served(t, addr, net.IPv4(127, 0, 0, 1))
		if pkt := c.read(time.Millisecond); pkt != nil {
			t.Errorf("peer %d answered: %v", i, pkt)
		}
		if n := s.peerCount(); n != udpPeersPerUser {
			t.Errorf("peer count = %d, want %d", n, udpPeersPerUser)
		}
	}
}

func TestUDPPeerMustJoin(t *testing.T) {
	s, addr := startUDPServer(t)
	s.Lock()
	s.joinTimeout = 100 * time.Millisecond
	s.Unlock()
	c := newUDPClient(t, addr, "U1")
	if !c.hello("12345678") {
		t.Fatal("hello rejected")
	}
	c.send()
	waitPeerCount(t, s, 1)
	waitPeerCount(t, s, 0)
}
