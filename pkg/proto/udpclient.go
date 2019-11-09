package proto

import (
	"context"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	pb "github.com/golang/protobuf/proto"
)

type UDPClient struct {
	mtx    sync.Mutex
	conn   *net.UDPConn
	onRead map[string]func([]byte, *net.UDPAddr)

	pbufMtx sync.Mutex
	pbuf    *pb.Buffer
}

func NewUDPClient(conn *net.UDPConn) *UDPClient {
	return &UDPClient{
		conn:   conn,
		onRead: map[string]func([]byte, *net.UDPAddr){},
		pbuf:   pb.NewBuffer(nil),
	}
}

func (c *UDPClient) SubscribePacket(key string, onPacket func(*Packet, *net.UDPAddr)) {
	pbuf := pb.NewBuffer(nil)
	onRead := func(b []byte, addr *net.UDPAddr) {
		pkt := GetPacket()
		defer PutPacket(pkt)
		pbuf.SetBuf(b)
		if err := pbuf.Unmarshal(pkt); err != nil {
			log.Println("SubscribePacket proto.Unmarshal error", err)
			return
		}
		// onRead は 呼び出し元で mtx.Lock している
		onPacket(pkt, addr)
	}
	c.Subscribe(key, onRead)
}

func (c *UDPClient) Subscribe(key string, onRead func([]byte, *net.UDPAddr)) {
	c.mtx.Lock()
	c.onRead[key] = onRead
	c.mtx.Unlock()
}

func (c *UDPClient) Unsubscribe(key string) {
	c.mtx.Lock()
	delete(c.onRead, key)
	c.mtx.Unlock()
}

func (c *UDPClient) ReadLoop(ctx context.Context) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, addr, err := c.conn.ReadFromUDP(buf)
			if err != nil {
				if strings.Contains(err.Error(), "use of closed network connection") {
					log.Println("udp conn closed", err)
					return
				}
				if err, ok := err.(net.Error); ok {
					if err.Timeout() || err.Temporary() {
						continue
					}
				}
				log.Println(err)
			} else if 0 < n {
				c.mtx.Lock()
				for _, f := range c.onRead {
					f(buf[:n], addr)
				}
				c.mtx.Unlock()
			}
		}
	}
}

func (c *UDPClient) SendPacket(pkt *Packet) {
	c.pbufMtx.Lock()
	c.pbuf.Reset()
	defer c.pbufMtx.Unlock()
	if err := c.pbuf.Marshal(pkt); err != nil {
		log.Println("Failed to send packet. Marshal error", err)
		return
	}
	n, err := c.conn.Write(c.pbuf.Bytes())
	if err != nil {
		log.Println("Failed to write packet.", err)
	} else if n != len(c.pbuf.Bytes()) {
		log.Println("Attempt to write", len(c.pbuf.Bytes()), "bytes but", n, "byte wrote")
	}
}

func (c *UDPClient) SendPacketTo(pkt *Packet, addr *net.UDPAddr) {
	c.pbufMtx.Lock()
	c.pbuf.Reset()
	defer c.pbufMtx.Unlock()
	if err := c.pbuf.Marshal(pkt); err != nil {
		log.Println("Failed to send packet. Marshal error", err)
		return
	}
	if n, err := c.conn.WriteToUDP(c.pbuf.Bytes(), addr); err != nil {
		// log.Println("Failed to write packet.", err, pkt)
	} else if n != len(c.pbuf.Bytes()) {
		log.Println("Attempt to write", len(c.pbuf.Bytes()), "bytes but", n, "byte wrote")
	}
}

func genPingPacket(userID string) *Packet {
	pkt := GetPacket()
	pkt.Type = MessageType_Ping.Enum()
	pkt.PingData = &PingMessage{
		Timestamp: pb.Int64(time.Now().UnixNano()),
		UserId:    pb.String(userID),
	}
	return pkt
}

func (c *UDPClient) SendPing(userID string, addr *net.UDPAddr) {
	pkt := genPingPacket(userID)
	c.SendPacket(pkt)
	PutPacket(pkt)
}

func (c *UDPClient) SendPingTo(userID string, addr *net.UDPAddr) {
	pkt := genPingPacket(userID)
	c.SendPacketTo(pkt, addr)
	PutPacket(pkt)
}

func (c *UDPClient) SendPingToAddr(userID string, addr string) {
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		log.Println("Failed to resolve udp address", err)
		return
	}
	c.SendPingTo(userID, udpAddr)
}

func genPongPacket(userID string, pktPing *Packet) *Packet {
	pkt := GetPacket()
	pkt.Type = MessageType_Pong.Enum()
	pkt.PongData = &PongMessage{
		Timestamp: pb.Int64(pktPing.GetPingData().GetTimestamp()),
		UserId:    pb.String(userID),
	}
	return pkt
}

func (c *UDPClient) SendPong(pktPing *Packet, userID string) {
	pkt := genPongPacket(userID, pktPing)
	c.SendPacket(pkt)
	PutPacket(pkt)
}

func (c *UDPClient) SendPongTo(pktPing *Packet, userID string, addr *net.UDPAddr) {
	pkt := genPongPacket(userID, pktPing)
	c.SendPacketTo(pkt, addr)
	PutPacket(pkt)
}
