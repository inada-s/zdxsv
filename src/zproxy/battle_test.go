package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"

	"zdxsv/pkg/battle"
	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/proto"
)

// startBattleServer runs the real battle UDP server with U1 (12345678) and U2 (87654321) waiting.
func startBattleServer(t *testing.T) *net.UDPAddr {
	t.Helper()
	logic := battle.NewLogic()
	var reply int
	logic.NotifyBattleUsers(&battlerpc.BattleInfoArgs{Users: []battlerpc.User{
		{UserID: "U1", SessionID: "12345678"}, {UserID: "U2", SessionID: "87654321"},
	}}, &reply)
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go battle.NewUDPServer(logic).Serve(c)
	return c.LocalAddr().(*net.UDPAddr)
}

// battleProxy is one zproxy in ServeBattle with a fake PS2 on its TCP side.
type battleProxy struct {
	z    *Zproxy
	ps2  *net.TCPConn
	done chan error
	addr *net.UDPAddr // its UDP side
}

func newBattleProxy(t *testing.T, sv *net.UDPAddr, userID, sessionID, otherID string) *battleProxy {
	t.Helper()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		udp.Close()
	})
	udpcl := proto.NewUDPClient(udp)
	go udpcl.ReadLoop(ctx)

	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ps2, err := net.DialTCP("tcp4", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps2.Close() })
	s, err := ln.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	z := NewZproxy()
	z.udpcl = udpcl
	z.ps2cl = &PS2Conn{s}
	z.userID = userID
	z.sessionID = sessionID
	z.svAddr = sv
	z.otherIDs = []string{otherID}
	return &battleProxy{z: z, ps2: ps2, done: make(chan error, 1), addr: udp.LocalAddr().(*net.UDPAddr)}
}

func (p *battleProxy) start() {
	go func() { p.done <- p.z.ServeBattle() }()
}

func (p *battleProxy) read(t *testing.T, n int) []byte {
	t.Helper()
	p.ps2.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, n)
	if _, err := io.ReadFull(p.ps2, b); err != nil {
		t.Fatalf("%s ps2 read %d bytes: %v", p.z.userID, n, err)
	}
	return b
}

// noMore checks the PS2 gets nothing more for a while.
func (p *battleProxy) noMore(t *testing.T) {
	t.Helper()
	p.ps2.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	b := make([]byte, 64)
	if n, err := p.ps2.Read(b); err == nil {
		t.Errorf("%s ps2 got extra % x", p.z.userID, b[:n])
	}
}

func (p *battleProxy) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-p.done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatalf("%s ServeBattle did not return", p.z.userID)
		return nil
	}
}

var ps2Greeting, _ = hex.DecodeString("280110310000000100ffffff")

// ps2Join is the PS2's first data: 130, then the encoded session id at bytes 12..22.
func ps2Join(encoded string) []byte {
	b := append(make([]byte, 12), encoded...)
	b[0] = 130
	return b
}

var finData = []byte{0x04, 0xF0, 0x00, 0x00}

// frame is s as one PS2 frame: its first byte is the frame size.
func frame(s string) []byte {
	return append([]byte{byte(len(s) + 1)}, s...)
}

func startPair(t *testing.T, p2p bool) (*battleProxy, *battleProxy) {
	t.Helper()
	sv := startBattleServer(t)
	a := newBattleProxy(t, sv, "U1", "12345678", "U2")
	b := newBattleProxy(t, sv, "U2", "87654321", "U1")
	if p2p {
		a.z.p2pAddr = map[string]*net.UDPAddr{"U2": b.addr}
		b.z.p2pAddr = map[string]*net.UDPAddr{"U1": a.addr}
	}
	for _, p := range []*battleProxy{a, b} {
		p.start()
		if got := p.read(t, len(ps2Greeting)); !bytes.Equal(got, ps2Greeting) {
			t.Fatalf("greeting = % x", got)
		}
	}
	// "0123505679" parses to "12345678": see battle.ParseSessionID
	a.ps2.Write(ps2Join("0123505679"))
	b.ps2.Write(ps2Join("0876604322"))
	time.Sleep(200 * time.Millisecond) // both joined before relaying
	return a, b
}

func TestServeBattleRelay(t *testing.T) {
	for _, tc := range []struct {
		name string
		p2p  bool
	}{{"server only", false}, {"server and p2p", true}} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := startPair(t, tc.p2p)

			a.ps2.Write(frame("from-a-1"))
			if got := b.read(t, 9); !bytes.Equal(got, frame("from-a-1")) {
				t.Errorf("b got %q", got)
			}
			b.ps2.Write(frame("from-b"))
			if got := a.read(t, 7); !bytes.Equal(got, frame("from-b")) {
				t.Errorf("a got %q", got)
			}
			time.Sleep(50 * time.Millisecond)
			a.ps2.Write(frame("from-a-2"))
			if got := b.read(t, 9); !bytes.Equal(got, frame("from-a-2")) {
				t.Errorf("b got %q", got)
			}
			// Each message reaches the PS2 once, whichever path it took.
			a.noMore(t)
			b.noMore(t)

			// Fin data ends the sender's battle, also in one read with a frame.
			a.ps2.Write(append(frame("last"), finData...))
			if err := a.wait(t); err != nil {
				t.Errorf("a ServeBattle = %v", err)
			}
		})
	}
}

func TestServeBattleUnknownSession(t *testing.T) {
	sv := startBattleServer(t)
	p := newBattleProxy(t, sv, "U9", "99999999", "U1")
	p.start()
	if err := p.wait(t); err == nil {
		t.Error("ServeBattle with an unknown session returned nil")
	}
}

func TestServeTestBattle(t *testing.T) {
	p := newBattleProxy(t, nil, "U1", "", "")
	p.z.testBattle = true
	p.start()
	if got := p.read(t, len(ps2Greeting)); !bytes.Equal(got, ps2Greeting) {
		t.Fatalf("greeting = % x", got)
	}
	p.ps2.Write(ps2Join("0123505679"))
	time.Sleep(50 * time.Millisecond) // the join is a read of its own
	p.ps2.Write(append(frame("anything"), finData...))
	if err := p.wait(t); err != nil {
		t.Errorf("ServeBattle = %v", err)
	}
	// The proxy closed the PS2 connection.
	p.ps2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := p.ps2.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("ps2 read after fin = %v, want EOF", err)
	}
}
