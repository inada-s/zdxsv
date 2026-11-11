package main

import (
	"context"
	"net"
	"testing"
	"time"

	"zproxy/battle"
	"zproxy/stun"
)

func listenUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUDPClient(t *testing.T) {
	aConn, bConn := listenUDP(t), listenUDP(t)
	a, b := NewUDPClient(aConn), NewUDPClient(bConn)
	aAddr := aConn.LocalAddr().(*net.UDPAddr)
	bAddr := bConn.LocalAddr().(*net.UDPAddr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	for _, c := range []*UDPClient{a, b} {
		go func(c *UDPClient) { c.ReadLoop(ctx); done <- struct{}{} }(c)
	}

	type recv struct {
		pkt  any
		addr *net.UDPAddr
	}
	got := make(chan recv, 16)
	for _, c := range []*UDPClient{a, b} {
		c.SubscribePacket("pb", func(p stun.Packet, addr *net.UDPAddr) { got <- recv{p, addr} })
		c.SubscribeBattle("battle", func(p battle.Packet, addr *net.UDPAddr) { got <- recv{p, addr} })
	}
	next := func() recv {
		t.Helper()
		select {
		case r := <-got:
			return r
		case <-time.After(2 * time.Second):
			t.Fatal("no packet")
		}
		return recv{}
	}

	a.SendPing("A", bAddr.String())
	r := next()
	ping, ok := r.pkt.(stun.Packet)
	if !ok || ping.Type != stun.Ping || ping.UserID != "A" || ping.Timestamp == 0 {
		t.Fatalf("ping = %v", r.pkt)
	}
	if !isSameAddr(r.addr, aAddr) {
		t.Errorf("ping from %v, want %v", r.addr, aAddr)
	}

	b.SendPong(ping, "B", r.addr)
	r = next()
	pong, ok := r.pkt.(stun.Packet)
	if !ok || pong.Type != stun.Pong || pong.UserID != "B" || pong.Timestamp != ping.Timestamp {
		t.Fatalf("pong = %v", r.pkt)
	}

	a.Send(battle.AppendBattle(nil, 7, 3, []battle.Message{{UserID: "A", Seq: 1, Body: []byte{1, 2}}}), bAddr)
	r = next()
	bp, ok := r.pkt.(battle.Packet)
	if !ok || bp.Kind != battle.KindBattle || bp.Seq != 7 || bp.Ack != 3 || len(bp.Messages) != 1 || bp.Messages[0].UserID != "A" {
		t.Fatalf("battle = %+v", r.pkt)
	}

	a.SendPing("x", "not an address")
	a.Send([]byte{0xFF, 0xFF, 0xFF}, bAddr)
	b.Unsubscribe("pb")
	a.SendPing("after-unsubscribe", bAddr.String())
	select {
	case r := <-got:
		t.Errorf("got %+v", r.pkt)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	aConn.Close()
	bConn.Close()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("ReadLoop did not return after close")
		}
	}
}
