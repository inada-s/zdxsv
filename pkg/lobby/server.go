package lobby

import (
	"net"
	"sync"
	"time"
	"zdxsv/pkg/lobby/message"
	"zdxsv/pkg/proto"

	"github.com/golang/glog"
	pb "github.com/golang/protobuf/proto"
)

type PeerFactory interface {
	NewPeer(conn *Conn) Peer
}

type Peer interface {
	OnOpen()
	OnMessage(*message.Message)
	OnClose()
}

type Server struct {
	conn *net.TCPConn
	pf   PeerFactory
}

func NewServer(pf PeerFactory) *Server {
	return &Server{
		pf: pf,
	}
}

func (s *Server) ServeUDPStunServer(addr string) error {
	glog.Infoln("Start UDPStun", addr)
	for {
		udpAddr, err := net.ResolveUDPAddr("udp4", addr)
		if err != nil {
			return err
		}
		udpConn, err := net.ListenUDP("udp4", udpAddr)
		if err != nil {
			return err
		}
		defer udpConn.Close()

		req := new(proto.Packet)
		res := new(proto.Packet)
		buf := make([]byte, 4096)
		for {
			n, addr, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				glog.Errorln(err)
				continue
			}
			if err := pb.Unmarshal(buf[:n], req); err != nil {
				glog.Errorln(err)
				continue
			}
			switch req.GetType() {
			case proto.MessageType_Ping:
				res.Type = proto.MessageType_Pong.Enum()
				res.PongData = &proto.PongMessage{
					PublicAddr: pb.String(addr.String()),
					UserId:     pb.String("SERVER"),
					Timestamp:  pb.Int64(req.GetPingData().GetTimestamp()),
				}
				data, err := pb.Marshal(res)
				if err != nil {
					glog.Errorln(err)
					continue
				}
				udpConn.WriteToUDP(data, addr)
			default:
				glog.Warningln("unexpected packet received", req)
			}
		}
	}
}

func (s *Server) ListenAndServe(addr string) error {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return err
	}
	listner, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}
	for {
		tcpConn, err := listner.AcceptTCP()
		if err != nil {
			glog.Errorln(err)
			continue
		}
		glog.Infoln("A new tcp connection open.", tcpConn.RemoteAddr())
		conn := NewConn(tcpConn)
		conn.peer = s.pf.NewPeer(conn)
		go conn.serve()
	}
	return nil
}

type Conn struct {
	conn *net.TCPConn
	peer Peer

	chWrite    chan bool
	chDispatch chan bool
	chQuit     chan interface{}

	mOutbuf sync.Mutex
	outbuf  []byte

	mInbuf sync.Mutex
	inbuf  []byte
}

func NewConn(conn *net.TCPConn) *Conn {
	return &Conn{
		conn:       conn,
		chWrite:    make(chan bool, 1),
		chDispatch: make(chan bool, 1),
		chQuit:     make(chan interface{}, 1),
		outbuf:     make([]byte, 0, 1024),
		inbuf:      make([]byte, 0, 1024),
	}
}

func (c *Conn) serve() {
	c.peer.OnOpen()
	go c.dispatchLoop()
	go c.writeLoop()
	c.readLoop()
	c.peer.OnClose()
	close(c.chQuit)
	c.conn.Close()
}

func (c *Conn) SendMessage(msg *message.Message) {
	glog.V(2).Infof("\t->%v %v \n", c.Address(), msg)
	c.mOutbuf.Lock()
	c.outbuf = append(c.outbuf, msg.Serialize()...)
	c.mOutbuf.Unlock()
	select {
	case c.chWrite <- true:
	default:
	}
}

func (c *Conn) Address() string {
	return c.conn.RemoteAddr().String()
}

func (c *Conn) readLoop() {
	buf := make([]byte, 4096)
	for {
		c.conn.SetReadDeadline(time.Now().Add(time.Minute * 30))
		n, err := c.conn.Read(buf)
		if err != nil {
			glog.Infoln("TCP conn error:", err)
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

func (c *Conn) writeLoop() {
	buf := make([]byte, 0, 128)
	for {
		select {
		case <-c.chQuit:
			return
		case <-c.chWrite:
			c.mOutbuf.Lock()
			if len(c.outbuf) == 0 {
				c.mOutbuf.Unlock()
				continue
			}
			buf = append(buf, c.outbuf...)
			c.outbuf = c.outbuf[:0]
			c.mOutbuf.Unlock()

			sum := 0
			size := len(buf)
			for sum < size {
				c.conn.SetWriteDeadline(time.Now().Add(time.Second * 10))
				n, err := c.conn.Write(buf[sum:])
				if err != nil {
					glog.Errorf("%v write error: %v\n", c.Address(), err)
					break
				}
				sum += n
			}
			buf = buf[:0]
		}
	}
}

func (c *Conn) dispatchLoop() {
	for {
		select {
		case <-c.chQuit:
			return
		case <-c.chDispatch:
			c.mInbuf.Lock()
			for len(c.inbuf) >= message.HeaderSize {
				n, msg := message.Deserialize(c.inbuf)
				c.inbuf = c.inbuf[n:]
				if msg != nil {
					glog.V(2).Infof("\t<-%v %v\n", c.Address(), msg)
					c.peer.OnMessage(msg)
				}
			}
			c.mInbuf.Unlock()
		}
	}
}
