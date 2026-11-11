package main

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// tcpPair starts a TCPPeer for one accepted connection and returns the client side.
func tcpPair(t *testing.T, ln *net.TCPListener, mcs *Mcs) *net.TCPConn {
	t.Helper()
	c, err := net.DialTCP("tcp", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ln.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	go NewTCPPeer(s).Serve(mcs)
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
	mcs := NewMcs()
	mcs.AddBattle([]McsUser{
		{UserID: "U1", SessionID: "12345678"}, {UserID: "U2", SessionID: "87654321"},
	})
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	greeting := []byte{0x28, 0x01, 0x10, 0x31, 0, 0, 0, 1, 0, 0xff, 0xff, 0xff}
	c1 := tcpPair(t, ln, mcs)
	c2 := tcpPair(t, ln, mcs)
	for _, c := range []net.Conn{c1, c2} {
		if got := readN(t, c, 12); !bytes.Equal(got, greeting) {
			t.Fatalf("greeting = % x", got)
		}
	}
	// "0123505679" parses to "12345678": see ParseSessionID
	c1.Write(hello("0123505679"))
	c2.Write(hello("0876604322"))
	waitJoined(t, mcs, 2)

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
	c := tcpPair(t, ln, NewMcs())
	readN(t, c, 12)
	c.Write(hello("0123505679"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("read after unknown session = %d, %v; want EOF", n, err)
	}
}

// joinedTCPClient returns a client connection whose server-side TCPPeer has joined a room.
func joinedTCPClient(t *testing.T) net.Conn {
	t.Helper()
	mcs := NewMcs()
	mcs.AddBattle([]McsUser{{UserID: "U1", SessionID: "12345678"}})
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	c, err := net.DialTCP("tcp", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	s, err := ln.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	go NewTCPPeer(s).Serve(mcs)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, make([]byte, 12)); err != nil {
		t.Fatal("greeting:", err)
	}
	// "0123505679" is session "12345678" (ParseSessionID)
	c.Write(append(make([]byte, 12), "0123505679"...))
	waitJoined(t, mcs, 1)
	return c
}

func expectServerClose(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("read = %d, %v; want EOF (server closed)", n, err)
	}
}

func TestTCPFinDataCloses(t *testing.T) {
	c := joinedTCPClient(t)
	c.Write([]byte{4, 240, 0, 0})
	expectServerClose(t, c)
}

func TestTCPZeroSizeFrameCloses(t *testing.T) {
	c := joinedTCPClient(t)
	c.Write([]byte{0})
	expectServerClose(t, c)
}
