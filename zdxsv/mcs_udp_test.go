package main

import (
	"bytes"
	"encoding/json"
	"net"
	"reflect"
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
}

func newUDPClient(t *testing.T, server *net.UDPAddr, userID string) *udpClient {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
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
	return &pkt
}

func (c *udpClient) hello(sessionID string) bool {
	c.t.Helper()
	c.write(AppendHello(nil, sessionID))
	pkt := c.read(2 * time.Second)
	if pkt == nil || pkt.Kind != KindHelloAnswer || pkt.SessionID != sessionID {
		c.t.Fatalf("hello reply = %v", pkt)
	}
	return pkt.OK
}

func (c *udpClient) send(bodies ...[]byte) {
	c.t.Helper()
	for _, b := range bodies {
		c.sent = append(c.sent, McsMessage{UserID: c.userID, Seq: uint32(len(c.sent) + 1), Body: b})
	}
	c.write(AppendBattle(nil, uint32(len(c.sent)), 0, c.sent))
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
		if !c.hello("12345678") {
			t.Fatal("waiting session rejected")
		}
	}
	waitPeerCount(t, s, 1)
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
