package main

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"zdxsv/zdxsv/proto"

	pb "github.com/golang/protobuf/proto"
	"go.uber.org/zap"
)

type LbsPeerFactory interface {
	NewPeer(conn *LbsConn) LbsConnPeer
}

type LbsConnPeer interface {
	OnOpen()
	OnMessage(*LbsMessage)
	OnClose()
}

type LbsServer struct {
	conn net.Conn
	pf   LbsPeerFactory
}

func NewLbsServer(pf LbsPeerFactory) *LbsServer {
	return &LbsServer{
		pf: pf,
	}
}

func (s *LbsServer) ListenAndServe(addr string) error {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return err
	}
	listner, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}
	return s.Serve(listner)
}

// Serve accepts connections on l until l is closed.
func (s *LbsServer) Serve(l net.Listener) error {
	for {
		tcpConn, err := l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return err
		}
		if err != nil {
			logger.Error("accept failed", zap.Error(err))
			continue
		}
		logger.Info("a new tcp connection open", zap.Stringer("addr", tcpConn.RemoteAddr()))
		go s.ServeConn(tcpConn)
	}
}

// ServeConn serves one client connection until it closes.
func (s *LbsServer) ServeConn(c net.Conn) {
	conn := NewLbsConn(c)
	conn.peer = s.pf.NewPeer(conn)
	conn.serve()
}

type LbsConn struct {
	conn net.Conn
	peer LbsConnPeer

	chWrite    chan bool
	chClose    chan bool
	chDispatch chan bool
	chQuit     chan interface{}

	mOutbuf sync.Mutex
	outbuf  []byte

	mInbuf sync.Mutex
	inbuf  []byte
}

func NewLbsConn(conn net.Conn) *LbsConn {
	return &LbsConn{
		conn:       conn,
		chWrite:    make(chan bool, 1),
		chClose:    make(chan bool, 1),
		chDispatch: make(chan bool, 1),
		outbuf:     make([]byte, 0, 1024),
		inbuf:      make([]byte, 0, 1024),
	}
}

func (c *LbsConn) serve() {
	defer c.conn.Close()
	defer c.peer.OnClose()

	ctx, cancel := context.WithCancel(context.Background())

	go c.dispatchLoop(ctx, cancel)
	go c.writeLoop(ctx, cancel)
	go c.readLoop(ctx, cancel)

	c.peer.OnOpen()
	<-ctx.Done()
}

func (c *LbsConn) SendMessage(msg *LbsMessage) {
	logger.Debug("send", zap.Any("addr", c.Address()), zap.Any("msg", msg))
	c.mOutbuf.Lock()
	c.outbuf = append(c.outbuf, msg.Serialize()...)
	c.mOutbuf.Unlock()
	select {
	case c.chWrite <- true:
	default:
	}
}

func (c *LbsConn) Address() string {
	return c.conn.RemoteAddr().String()
}

func (c *LbsConn) readLoop(ctx context.Context, cancel func()) {
	defer cancel()

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		c.conn.SetReadDeadline(time.Now().Add(time.Minute * 30))
		n, err := c.conn.Read(buf)
		if err != nil {
			logger.Info("tcp conn error", zap.Error(err))
			return
		}
		if n == 0 {
			logger.Info("tcp read zero")
			return
		}
		c.mInbuf.Lock()
		c.inbuf = append(c.inbuf, buf[:n]...)
		c.mInbuf.Unlock()

		select {
		case c.chDispatch <- true:
		default:
		}
	}
}

func (c *LbsConn) writeLoop(ctx context.Context, cancel func()) {
	defer cancel()

	buf := make([]byte, 0, 128)
	flush := func() {
		c.mOutbuf.Lock()
		buf = append(buf, c.outbuf...)
		c.outbuf = c.outbuf[:0]
		c.mOutbuf.Unlock()

		sum := 0
		size := len(buf)
		for sum < size {
			c.conn.SetWriteDeadline(time.Now().Add(time.Second * 10))
			n, err := c.conn.Write(buf[sum:])
			if err != nil {
				logger.Error("tcp write error", zap.Any("addr", c.Address()), zap.Error(err))
				break
			}
			sum += n
		}
		buf = buf[:0]
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.chWrite:
			flush()
		case <-c.chClose:
			flush()
			return
		}
	}
}

// Close closes the connection once the messages sent before it are written.
func (c *LbsConn) Close() {
	select {
	case c.chClose <- true:
	default:
	}
}

func (c *LbsConn) dispatchLoop(ctx context.Context, cancel func()) {
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.chDispatch:
			c.mInbuf.Lock()
			for len(c.inbuf) >= HeaderSize {
				n, msg := Deserialize(c.inbuf)
				c.inbuf = c.inbuf[n:]

				if msg != nil {
					logger.Debug("recv", zap.Any("addr", c.Address()), zap.Any("msg", msg))
					c.peer.OnMessage(msg)
				}
				if n == 0 {
					logger.Error("got zero byte msg", zap.Any("addr", c.Address()))
					return
				}
			}
			c.mInbuf.Unlock()
		}
	}
}

// UDPTestUserID in a Ping to the main STUN socket asks for the reachability test:
// the Pong is also sent from the test socket, which the client never sent to, so it
// only arrives when the client's port takes packets from any source (port forwarding,
// UPnP, full/address-restricted cone NAT).
const UDPTestUserID = "udptest"

// ServeUDPStunServer answers Ping with Pong (the sender's public address) on addr, and
// on addr's port + 1 (the test socket). Clients compare both public addresses to tell
// a cone NAT (same mapped port) from a symmetric one.
func (s *LbsServer) ServeUDPStunServer(addr string) error {
	logger.Info("start UDPStun", zap.Any("addr", addr))
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	testAddr := &net.UDPAddr{IP: udpAddr.IP, Port: udpAddr.Port + 1}
	test, err := net.ListenUDP("udp4", testAddr)
	if err != nil {
		logger.Error("UDPStun test socket failed", zap.Error(err))
		test = nil
	} else {
		logger.Info("start UDPStun test", zap.Any("addr", testAddr))
		defer test.Close()
		go serveUDPStun(test, nil)
	}

	serveUDPStun(conn, test)
	return nil
}

// ServeUDPStun answers Ping with Pong on addr, without the test socket.
func (s *LbsServer) ServeUDPStun(addr string) error {
	logger.Info("start UDPStun", zap.Any("addr", addr))
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	serveUDPStun(conn, nil)
	return nil
}

// serveUDPStun answers Pings.
func serveUDPStun(conn, test *net.UDPConn) {
	buf := make([]byte, 64<<10)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			logger.Error("UDPStun read failed", zap.Error(err))
			if ne, ok := err.(net.Error); ok && !ne.Timeout() && !ne.Temporary() {
				return
			}
			continue
		}
		req := new(proto.Packet)
		if err := pb.Unmarshal(buf[:n], req); err != nil {
			logger.Error("UDPStun unmarshal failed", zap.Error(err))
			continue
		}
		data, test2 := udpStunAnswer(req, addr)
		if data == nil {
			continue
		}
		conn.WriteToUDP(data, addr)
		if test2 && test != nil {
			test.WriteToUDP(data, addr)
		}
	}
}

// udpStunAnswer returns the Pong for a Ping packet (nil for anything else), and
// whether the Ping asked for the reachability test.
func udpStunAnswer(req *proto.Packet, addr *net.UDPAddr) ([]byte, bool) {
	if req.GetType() != proto.MessageType_Ping {
		logger.Warn("unexpected packet received", zap.Any("req", req))
		return nil, false
	}
	res := &proto.Packet{
		Type: proto.MessageType_Pong.Enum(),
		PongData: &proto.PongMessage{
			PublicAddr: pb.String(addr.String()),
			UserId:     pb.String("SERVER"),
			Timestamp:  pb.Int64(req.GetPingData().GetTimestamp()),
		},
	}
	data, err := pb.Marshal(res)
	if err != nil {
		logger.Error("UDPStun write failed", zap.Error(err))
		return nil, false
	}
	return data, req.GetPingData().GetUserId() == UDPTestUserID
}
