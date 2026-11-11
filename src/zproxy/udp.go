package main

import (
	"context"
	"errors"
	"log"
	"net"
	"sync"
	"time"

	"zdxsv/pkg/battle"
	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
)

// UDPClient fans each datagram on one socket out to its subscribers.
// Subscribers run on the read goroutine and must not keep the bytes.
type UDPClient struct {
	conn   *net.UDPConn
	mtx    sync.Mutex
	onRead map[string]func([]byte, *net.UDPAddr)
}

func NewUDPClient(conn *net.UDPConn) *UDPClient {
	return &UDPClient{conn: conn, onRead: map[string]func([]byte, *net.UDPAddr){}}
}

func (c *UDPClient) Subscribe(key string, onRead func([]byte, *net.UDPAddr)) {
	c.mtx.Lock()
	c.onRead[key] = onRead
	c.mtx.Unlock()
}

// SubscribePacket subscribes to protobuf packets (Ping/Pong).
func (c *UDPClient) SubscribePacket(key string, onPacket func(*proto.Packet, *net.UDPAddr)) {
	c.Subscribe(key, func(b []byte, addr *net.UDPAddr) {
		if battle.IsWirePacket(b) {
			return
		}
		pkt := new(proto.Packet)
		if err := pb.Unmarshal(b, pkt); err != nil {
			log.Println("udp: bad packet:", err)
			return
		}
		onPacket(pkt, addr)
	})
}

func (c *UDPClient) SubscribeBattle(key string, onPacket func(battle.Packet, *net.UDPAddr)) {
	c.Subscribe(key, func(b []byte, addr *net.UDPAddr) {
		if !battle.IsWirePacket(b) {
			return
		}
		pkt, err := battle.ParsePacket(b)
		if err != nil {
			log.Println("udp:", err)
			return
		}
		onPacket(pkt, addr)
	})
}

func (c *UDPClient) Unsubscribe(key string) {
	c.mtx.Lock()
	delete(c.onRead, key)
	c.mtx.Unlock()
}

func (c *UDPClient) ReadLoop(ctx context.Context) {
	buf := make([]byte, 4096)
	for ctx.Err() == nil {
		c.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := c.conn.ReadFromUDP(buf)
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil || n == 0 {
			continue
		}
		c.mtx.Lock()
		for _, f := range c.onRead {
			f(buf[:n], addr)
		}
		c.mtx.Unlock()
	}
}

func (c *UDPClient) Send(b []byte, addr *net.UDPAddr) {
	if _, err := c.conn.WriteToUDP(b, addr); err != nil {
		log.Println("udp: write:", err)
	}
}

func (c *UDPClient) SendPacket(pkt *proto.Packet, addr *net.UDPAddr) {
	b, err := pb.Marshal(pkt)
	if err != nil {
		log.Println("udp: marshal:", err)
		return
	}
	c.Send(b, addr)
}

func (c *UDPClient) SendPing(userID string, addr string) {
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		log.Println("udp:", err)
		return
	}
	c.SendPacket(&proto.Packet{
		Type:     proto.MessageType_Ping.Enum(),
		PingData: &proto.PingMessage{Timestamp: pb.Int64(time.Now().UnixNano()), UserId: pb.String(userID)},
	}, udpAddr)
}

func (c *UDPClient) SendPong(ping *proto.Packet, userID string, addr *net.UDPAddr) {
	c.SendPacket(&proto.Packet{
		Type:     proto.MessageType_Pong.Enum(),
		PongData: &proto.PongMessage{Timestamp: pb.Int64(ping.GetPingData().GetTimestamp()), UserId: pb.String(userID)},
	}, addr)
}
