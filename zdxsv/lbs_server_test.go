package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func stunPing(t *testing.T, c *net.UDPConn, to *net.UDPAddr, userID string) {
	data, err := json.Marshal(StunPacket{Type: StunPing, Timestamp: 42, UserID: userID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteToUDP(data, to); err != nil {
		t.Fatal(err)
	}
}

// stunPongs returns the source port -> Pong public_addr of what arrives within d.
func stunPongs(t *testing.T, c *net.UDPConn, d time.Duration) map[int]string {
	got := map[int]string{}
	buf := make([]byte, 4096)
	c.SetReadDeadline(time.Now().Add(d))
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return got
		}
		var res StunPacket
		if err := json.Unmarshal(buf[:n], &res); err != nil || res.Type != StunPong || res.Timestamp != 42 {
			t.Fatalf("bad answer %q", buf[:n])
		}
		got[from.Port] = res.PublicAddr
	}
}

// testdata/wire/pong.json is the answer to testdata/wire/ping.json from 203.0.113.1:8250.
func TestUDPStunFixture(t *testing.T) {
	ping, err := os.ReadFile("../testdata/wire/ping.json")
	if err != nil {
		t.Fatal(err)
	}
	pong, err := os.ReadFile("../testdata/wire/pong.json")
	if err != nil {
		t.Fatal(err)
	}
	got, test := udpStunAnswer(ping, &net.UDPAddr{IP: net.IPv4(203, 0, 113, 1), Port: 8250})
	if string(got) != strings.TrimSpace(string(pong)) || test {
		t.Errorf("answer %s %v, want %s", got, test, pong)
	}
	for _, b := range []string{`{"type":"pong","timestamp":1}`, `{"type":`, "\x08\x02\x5a\x02\x08\x01"} {
		if got, _ := udpStunAnswer([]byte(b), &net.UDPAddr{IP: net.IPv4(203, 0, 113, 1), Port: 8250}); got != nil {
			t.Errorf("answer to %q: %s", b, got)
		}
	}
}

func TestUDPStunServer(t *testing.T) {
	// a free port whose port + 1 is free too
	var port int
	for i := 0; i < 20 && port == 0; i++ {
		a, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		p := a.LocalAddr().(*net.UDPAddr).Port
		a.Close()
		if b, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p + 1}); err == nil {
			b.Close()
			port = p
		}
	}
	srv := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	go (&LbsServer{}).ServeUDPStunServer(srv.String())
	test := &net.UDPAddr{IP: srv.IP, Port: port + 1}

	// a client bound to port + 1 before the server would take its test socket
	waitFor(t, "the stun test socket", func() bool {
		probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		defer probe.Close()
		if probe.LocalAddr().(*net.UDPAddr).Port == test.Port {
			return false
		}
		stunPing(t, probe, test, "AAAAAA")
		return len(stunPongs(t, probe, 20*time.Millisecond)) > 0
	})
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	me := c.LocalAddr().String()

	// plain Ping: one Pong, from the main socket
	stunPing(t, c, srv, "AAAAAA")
	if got := stunPongs(t, c, 500*time.Millisecond); len(got) != 1 || got[port] != me {
		t.Fatalf("plain ping: %v want only {%d:%s}", got, port, me)
	}
	// reachability test: also a Pong from the test socket, never sent to
	stunPing(t, c, srv, "udptest") // UDPTestUserID
	if got := stunPongs(t, c, 500*time.Millisecond); len(got) != 2 || got[port] != me || got[port+1] != me {
		t.Fatalf("udptest ping: %v want {%d:%s %d:%s}", got, port, me, port+1, me)
	}
	// the test socket answers Pings itself (the second mapping for the NAT type)
	stunPing(t, c, test, "AAAAAA")
	if got := stunPongs(t, c, 500*time.Millisecond); len(got) != 1 || got[port+1] != me {
		t.Fatalf("test socket ping: %v want only {%d:%s}", got, port+1, me)
	}
}

type recordingPeer struct {
	msgs   chan *LbsMessage
	closed chan struct{}
}

func (p *recordingPeer) OnOpen()                      {}
func (p *recordingPeer) OnMessage(m *LbsMessage)      { p.msgs <- m }
func (p *recordingPeer) OnClose()                     { close(p.closed) }
func (p *recordingPeer) NewPeer(*LbsConn) LbsConnPeer { return p }

type idlePeer struct{}

func (idlePeer) OnOpen()                      {}
func (idlePeer) OnMessage(*LbsMessage)        {}
func (idlePeer) OnClose()                     {}
func (idlePeer) NewPeer(*LbsConn) LbsConnPeer { return idlePeer{} }

func setCaps(t *testing.T, max, perIP *int, m, p int) {
	t.Helper()
	oldMax, oldPerIP := *max, *perIP
	*max, *perIP = m, p
	t.Cleanup(func() { *max, *perIP = oldMax, oldPerIP })
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

func dialFrom(t *testing.T, ip string, ln net.Listener) net.Conn {
	t.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip)}}
	c, err := d.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// serverOpen is false once the server closed c, true while c stays open for d.
func serverOpen(c net.Conn, d time.Duration) bool {
	c.SetReadDeadline(time.Now().Add(d))
	_, err := io.ReadFull(c, make([]byte, 1))
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	return err == nil
}

func TestLbsConnCaps(t *testing.T) {
	setCaps(t, &lbsMaxConns, &lbsMaxConnsPerIP, 3, 2)
	ln := listenLoopback(t)
	go NewLbsServer(idlePeer{}).Serve(ln)

	open := func(ip string) bool { return serverOpen(dialFrom(t, ip, ln), 300*time.Millisecond) }
	a1 := dialFrom(t, "127.0.0.1", ln)
	if !serverOpen(a1, 300*time.Millisecond) || !open("127.0.0.1") {
		t.Fatal("first two connections from one IP refused")
	}
	if open("127.0.0.1") {
		t.Error("third connection from one IP accepted")
	}
	if !open("127.0.0.2") {
		t.Fatal("connection from a second IP refused")
	}
	if open("127.0.0.3") {
		t.Error("connection over the total cap accepted")
	}
	a1.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !open("127.0.0.1") {
		if time.Now().After(deadline) {
			t.Fatal("closed connection's slot not released")
		}
	}
}

func TestLbsConnMessageSplitAcrossReads(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	peer := &recordingPeer{msgs: make(chan *LbsMessage, 4), closed: make(chan struct{})}
	go NewLbsServer(peer).ServeConn(server)

	body := bytes.Repeat([]byte("x"), 20000)
	big := (&LbsMessage{Direction: ClientToServer, Category: CategoryCustom, Command: 0x9952, Body: body}).Serialize()
	small := (&LbsMessage{Direction: ClientToServer, Category: CategoryCustom, Command: 0x9950, Body: []byte("a=1\n")}).Serialize()
	for _, part := range [][]byte{big[:HeaderSize+10], big[HeaderSize+10 : 9000], append(big[9000:], small...)} {
		if _, err := client.Write(part); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	for _, want := range []struct {
		cmd  uint16
		size int
	}{{0x9952, len(body)}, {0x9950, 4}} {
		select {
		case m := <-peer.msgs:
			if m.Command != want.cmd || len(m.Body) != want.size {
				t.Fatalf("got command %#x with %d bytes, want %#x with %d", m.Command, len(m.Body), want.cmd, want.size)
			}
		case <-peer.closed:
			t.Fatalf("connection closed before command %#x", want.cmd)
		case <-time.After(5 * time.Second):
			t.Fatalf("no command %#x", want.cmd)
		}
	}
}
