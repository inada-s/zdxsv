package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"log"
	"math"
	"net"
	"sync"
	"time"
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

// SubscribePacket subscribes to Ping/Pong.
func (c *UDPClient) SubscribePacket(key string, onPacket func(StunPacket, *net.UDPAddr)) {
	c.Subscribe(key, func(b []byte, addr *net.UDPAddr) {
		if IsBattlePacket(b) {
			return
		}
		pkt, err := UnmarshalStun(b)
		if err != nil {
			log.Println("udp: bad packet:", err)
			return
		}
		onPacket(pkt, addr)
	})
}

func (c *UDPClient) SubscribeBattle(key string, onPacket func(BattlePacket, *net.UDPAddr)) {
	c.Subscribe(key, func(b []byte, addr *net.UDPAddr) {
		if !IsBattlePacket(b) {
			return
		}
		pkt, err := ParseBattlePacket(b)
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

func (c *UDPClient) SendPacket(pkt StunPacket, addr *net.UDPAddr) {
	c.Send(MarshalStun(pkt), addr)
}

func (c *UDPClient) SendPing(userID string, token int64, addr string) {
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		log.Println("udp:", err)
		return
	}
	c.SendPacket(StunPacket{Type: StunPing, Timestamp: token, UserID: userID}, udpAddr)
}

func (c *UDPClient) SendPong(ping StunPacket, userID string, addr *net.UDPAddr) {
	c.SendPacket(StunPacket{Type: StunPong, Timestamp: ping.Timestamp, UserID: userID}, addr)
}

// pingTTL is how long a Ping waits for its Pong.
const pingTTL = 10 * time.Second

// Pinger gives each Ping a random token in its Timestamp, which a Pong echoes.
// A Pong counts only if it echoes a pending token given for the user it names.
type Pinger struct {
	mtx     sync.Mutex
	pending map[int64]sentPing
}

type sentPing struct {
	userID string
	at     time.Time
}

func NewPinger() *Pinger {
	return &Pinger{pending: map[int64]sentPing{}}
}

// Token returns a new token for a Ping to userID.
func (p *Pinger) Token(userID string) int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	token := int64(binary.BigEndian.Uint64(b[:]) & math.MaxInt64)
	now := time.Now()
	p.mtx.Lock()
	defer p.mtx.Unlock()
	for t, s := range p.pending {
		if now.Sub(s.at) > pingTTL {
			delete(p.pending, t)
		}
	}
	p.pending[token] = sentPing{userID, now}
	return token
}

// Answer takes the token pong echoes and returns the round trip time.
func (p *Pinger) Answer(pong StunPacket) (time.Duration, bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	s, ok := p.pending[pong.Timestamp]
	if !ok || pong.Type != StunPong || s.userID != pong.UserID || time.Since(s.at) > pingTTL {
		return 0, false
	}
	delete(p.pending, pong.Timestamp)
	return time.Since(s.at), true
}
