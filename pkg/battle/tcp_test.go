package battle

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"zdxsv/pkg/battle/battlerpc"
)

// tcpPair starts a TCPPeer for one accepted connection and returns the client side.
func tcpPair(t *testing.T, ln *net.TCPListener, logic *Logic) *net.TCPConn {
	t.Helper()
	c, err := net.DialTCP("tcp", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ln.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	go NewTCPPeer(s).Serve(logic)
	t.Cleanup(func() { c.Close() })
	return c
}

func readN(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, n)
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return b
}

// hello is the client's first packet: the encoded session id at bytes 12..22.
func hello(encoded string) []byte {
	return append(make([]byte, 12), encoded...)
}

func TestTCPRelay(t *testing.T) {
	logic := NewLogic()
	var reply int
	logic.NotifyBattleUsers(&battlerpc.BattleInfoArgs{Users: []battlerpc.User{
		{UserID: "U1", SessionID: "12345678"}, {UserID: "U2", SessionID: "87654321"},
	}}, &reply)
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	greeting := []byte{0x28, 0x01, 0x10, 0x31, 0, 0, 0, 1, 0, 0xff, 0xff, 0xff}
	c1 := tcpPair(t, ln, logic)
	c2 := tcpPair(t, ln, logic)
	for _, c := range []net.Conn{c1, c2} {
		if got := readN(t, c, 12); !bytes.Equal(got, greeting) {
			t.Fatalf("greeting = % x", got)
		}
	}
	// "0123505679" parses to "12345678": see ParseSessionID
	c1.Write(hello("0123505679"))
	c2.Write(hello("0876604322"))
	time.Sleep(100 * time.Millisecond) // both joined before relaying

	// size-prefixed frames; a split frame is held until complete
	c1.Write([]byte{3, 0xA, 0xB, 4, 0xC})
	c1.Write([]byte{0xD, 0xE})
	if got := readN(t, c2, 3); !bytes.Equal(got, []byte{3, 0xA, 0xB}) {
		t.Errorf("first frame = % x", got)
	}
	if got := readN(t, c2, 4); !bytes.Equal(got, []byte{4, 0xC, 0xD, 0xE}) {
		t.Errorf("second frame = % x", got)
	}
	c2.Write([]byte{2, 0x55})
	if got := readN(t, c1, 2); !bytes.Equal(got, []byte{2, 0x55}) {
		t.Errorf("reverse frame = % x", got)
	}
}

func TestTCPUnknownSessionClosed(t *testing.T) {
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c := tcpPair(t, ln, NewLogic())
	readN(t, c, 12)
	c.Write(hello("0123505679"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("read after unknown session = %d, %v; want EOF", n, err)
	}
}
