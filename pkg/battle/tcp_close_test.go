package battle

import (
	"io"
	"net"
	"testing"
	"time"
)

// joinedTCPClient returns a client connection whose server-side TCPPeer has joined a room.
func joinedTCPClient(t *testing.T) net.Conn {
	t.Helper()
	logic := NewMcs()
	logic.AddBattle([]McsUser{{UserID: "U1", SessionID: "12345678"}})
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
	go NewTCPPeer(s).Serve(logic)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, make([]byte, 12)); err != nil {
		t.Fatal("greeting:", err)
	}
	// "0123505679" is session "12345678" (ParseSessionID)
	c.Write(append(make([]byte, 12), "0123505679"...))
	waitJoined(t, logic, 1)
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
