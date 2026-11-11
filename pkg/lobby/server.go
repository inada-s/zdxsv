package lobby

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
	"zdxsv/pkg/lobby/message"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
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
	conn net.Conn
	pf   PeerFactory
}

func NewServer(pf PeerFactory) *Server {
	return &Server{
		pf: pf,
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
	return s.Serve(listner)
}

// Serve accepts connections on l until l is closed.
func (s *Server) Serve(l net.Listener) error {
	for {
		tcpConn, err := l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return err
		}
		if err != nil {
			zlog.Error("accept failed", zap.Error(err))
			continue
		}
		zlog.Info("a new tcp connection open", zap.Stringer("addr", tcpConn.RemoteAddr()))
		go s.ServeConn(tcpConn)
	}
}

// ServeConn serves one client connection until it closes.
func (s *Server) ServeConn(c net.Conn) {
	conn := NewConn(c)
	conn.peer = s.pf.NewPeer(conn)
	conn.serve()
}

type Conn struct {
	conn net.Conn
	peer Peer

	chWrite    chan bool
	chClose    chan bool
	chDispatch chan bool
	chQuit     chan interface{}

	mOutbuf sync.Mutex
	outbuf  []byte

	mInbuf sync.Mutex
	inbuf  []byte
}

func NewConn(conn net.Conn) *Conn {
	return &Conn{
		conn:       conn,
		chWrite:    make(chan bool, 1),
		chClose:    make(chan bool, 1),
		chDispatch: make(chan bool, 1),
		outbuf:     make([]byte, 0, 1024),
		inbuf:      make([]byte, 0, 1024),
	}
}

func (c *Conn) serve() {
	defer c.conn.Close()
	defer c.peer.OnClose()

	ctx, cancel := context.WithCancel(context.Background())

	go c.dispatchLoop(ctx, cancel)
	go c.writeLoop(ctx, cancel)
	go c.readLoop(ctx, cancel)

	c.peer.OnOpen()
	<-ctx.Done()
}

func (c *Conn) SendMessage(msg *message.Message) {
	zlog.Debug("send", zap.Any("addr", c.Address()), zap.Any("msg", msg))
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

func (c *Conn) readLoop(ctx context.Context, cancel func()) {
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
			zlog.Info("tcp conn error", zap.Error(err))
			return
		}
		if n == 0 {
			zlog.Info("tcp read zero")
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

func (c *Conn) writeLoop(ctx context.Context, cancel func()) {
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
				zlog.Error("tcp write error", zap.Any("addr", c.Address()), zap.Error(err))
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
func (c *Conn) Close() {
	select {
	case c.chClose <- true:
	default:
	}
}

func (c *Conn) dispatchLoop(ctx context.Context, cancel func()) {
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.chDispatch:
			c.mInbuf.Lock()
			for len(c.inbuf) >= message.HeaderSize {
				n, msg := message.Deserialize(c.inbuf)
				c.inbuf = c.inbuf[n:]

				if msg != nil {
					zlog.Debug("recv", zap.Any("addr", c.Address()), zap.Any("msg", msg))
					c.peer.OnMessage(msg)
				}
				if n == 0 {
					zlog.Error("got zero byte msg", zap.Any("addr", c.Address()))
					return
				}
			}
			c.mInbuf.Unlock()
		}
	}
}
