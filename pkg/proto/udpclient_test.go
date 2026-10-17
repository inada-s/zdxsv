package proto

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/golang/protobuf/proto"
)

func listenUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type udpRecv struct {
	pkt  *Packet
	addr *net.UDPAddr
}

// subscribe forwards a copy of every packet; the client reuses the original after the callback.
func subscribe(c *UDPClient, key string) chan udpRecv {
	ch := make(chan udpRecv, 16)
	c.SubscribePacket(key, func(p *Packet, addr *net.UDPAddr) {
		ch <- udpRecv{pb.Clone(p).(*Packet), addr}
	})
	return ch
}

func recv(t *testing.T, ch chan udpRecv) udpRecv {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("no packet")
	}
	return udpRecv{}
}

func TestUDPClientPingPong(t *testing.T) {
	serverConn := listenUDP(t)
	clientConn, err := net.DialUDP("udp4", nil, serverConn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	server, client := NewUDPClient(serverConn), NewUDPClient(clientConn)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	for _, c := range []*UDPClient{server, client} {
		go func(c *UDPClient) { c.ReadLoop(ctx); done <- struct{}{} }(c)
	}
	sch, cch := subscribe(server, "s"), subscribe(client, "c")
	raw := make(chan []byte, 16)
	server.Subscribe("raw", func(b []byte, _ *net.UDPAddr) { raw <- append([]byte{}, b...) })

	client.SendPing("client", nil)
	ping := recv(t, sch)
	if ping.pkt.GetType() != MessageType_Ping || ping.pkt.GetPingData().GetUserId() != "client" || ping.pkt.GetPingData().GetTimestamp() == 0 {
		t.Fatalf("ping = %v", ping.pkt)
	}
	if ping.addr.String() != clientConn.LocalAddr().String() {
		t.Errorf("ping from %v, want %v", ping.addr, clientConn.LocalAddr())
	}
	<-raw

	server.SendPongTo(ping.pkt, "server", ping.addr)
	pong := recv(t, cch)
	if pong.pkt.GetType() != MessageType_Pong || pong.pkt.GetPongData().GetUserId() != "server" ||
		pong.pkt.GetPongData().GetTimestamp() != ping.pkt.GetPingData().GetTimestamp() {
		t.Fatalf("pong = %v", pong.pkt)
	}

	// SendPong and SendPingToAddr use the connected / resolved address
	client.SendPong(ping.pkt, "client2")
	if r := recv(t, sch); r.pkt.GetPongData().GetUserId() != "client2" {
		t.Errorf("SendPong = %v", r.pkt)
	}
	<-raw
	server.SendPingToAddr("server2", clientConn.LocalAddr().String())
	if r := recv(t, cch); r.pkt.GetPingData().GetUserId() != "server2" {
		t.Errorf("SendPingToAddr = %v", r.pkt)
	}
	server.SendPingToAddr("x", "not an address")

	// bytes that are not a Packet reach raw subscribers only
	clientConn.Write([]byte{0xFF, 0xFF, 0xFF})
	if b := <-raw; len(b) != 3 {
		t.Errorf("raw = % x", b)
	}
	server.Unsubscribe("s")
	client.SendPing("after-unsubscribe", nil)
	<-raw
	select {
	case r := <-sch:
		t.Errorf("unsubscribed handler got %v", r.pkt)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	serverConn.Close()
	clientConn.Close()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("ReadLoop did not return after close")
		}
	}
}
