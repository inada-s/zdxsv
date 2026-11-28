package main

import (
	"encoding/hex"
	"io"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

type McsTCPServer struct {
	mcs   *Mcs
	conns connLimiter
}

func NewTCPServer(mcs *Mcs) *McsTCPServer {
	return &McsTCPServer{
		mcs:   mcs,
		conns: connLimiter{max: tcpMaxConns, perIP: tcpMaxConnsPerIP},
	}
}

func (s *McsTCPServer) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

// Serve accepts connections on l until l is closed.
func (s *McsTCPServer) Serve(l net.Listener) error {
	for {
		conn, err := accept(l, "battle tcp")
		if err != nil {
			return err
		}
		ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		if !s.conns.acquire(ip) {
			floodLog.Warn("battle tcp connection refused: too many connections", zap.Stringer("addr", conn.RemoteAddr()))
			conn.Close()
			continue
		}
		go func() {
			defer s.conns.release(ip)
			NewTCPPeer(conn).Serve(s.mcs)
		}()
	}
}

type McsTCPPeer struct {
	BaseMcsPeer

	conn      net.Conn
	room      *McsRoom
	seq       uint32
	sendq     chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

var (
	tcpMaxConns      = 4096
	tcpMaxConnsPerIP = 16
	tcpJoinTimeout   = 10 * time.Second
	tcpSendQueueLen  = 1024
	tcpWriteTimeout  = 10 * time.Second
)

func NewTCPPeer(conn net.Conn) *McsTCPPeer {
	u := &McsTCPPeer{
		conn: conn,
		seq:  1,
		done: make(chan struct{}),
	}
	return u
}

func (u *McsTCPPeer) Close() error {
	var err error
	u.closeOnce.Do(func() {
		close(u.done)
		err = u.conn.Close()
	})
	return err
}

func (u *McsTCPPeer) Serve(mcs *Mcs) {
	floodLog.Info("tcp serve start", zap.Any("addr", u.Address()))
	defer floodLog.Info("tcp serve end", zap.Any("addr", u.Address()))
	greeting, _ := hex.DecodeString("280110310000000100ffffff")
	u.conn.SetWriteDeadline(time.Now().Add(tcpWriteTimeout))
	if _, err := u.conn.Write(greeting); err != nil {
		u.Close()
		return
	}
	u.readLoop(mcs)
	if u.room != nil {
		u.room.Leave(u)
		u.room = nil
	}
	u.Close()
}

func (u *McsTCPPeer) AddSendMessage(msg McsMessage) {
	u.AddSendData(msg.Body)
}

func (u *McsTCPPeer) AddSendData(data []byte) {
	select {
	case <-u.done:
	case u.sendq <- data:
	default:
		floodLog.Error("tcp send queue full", zap.Any("addr", u.Address()))
		u.Close()
	}
}

func (u *McsTCPPeer) writeLoop() {
	for {
		select {
		case <-u.done:
			return
		case data := <-u.sendq:
			u.conn.SetWriteDeadline(time.Now().Add(tcpWriteTimeout))
			if _, err := u.conn.Write(data); err != nil {
				floodLog.Error("tcp write error", zap.Any("addr", u.Address()), zap.Error(err))
				u.Close()
				return
			}
		}
	}
}

func (u *McsTCPPeer) Address() string {
	return u.conn.RemoteAddr().String()
}

func (u *McsTCPPeer) readLoop(mcs *Mcs) {
	buf := make([]byte, 1024)
	inbuf := make([]byte, 0, 128)
	joinDeadline := time.Now().Add(tcpJoinTimeout)

	for {
		if u.room == nil {
			u.conn.SetReadDeadline(joinDeadline)
		} else {
			u.conn.SetReadDeadline(time.Now().Add(time.Second * 10))
		}
		n, err := u.conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				floodLog.Error("tcp read error", zap.Any("addr", u.Address()), zap.Error(err))
			}
			return
		}
		if IsFinData(buf[:n]) {
			return
		}
		inbuf = append(inbuf, buf[:n]...)

		if u.room == nil {
			floodLog.Info("room nil", zap.Any("addr", u.Address()))
			if len(inbuf) < 22 {
				continue
			}
			value := string(inbuf[12:22])
			inbuf = inbuf[:0]
			sessionID, err := ParseSessionID(value)
			floodLog.Info("tcp session id", zap.Any("session_id", sessionID), zap.Error(err))
			if err != nil {
				return
			}
			u.sendq = make(chan []byte, tcpSendQueueLen)
			go u.writeLoop()
			u.room = mcs.Join(u, sessionID)
			if u.room == nil {
				floodLog.Info("failed to join room", zap.Any("user_id", u.UserID()), zap.Any("addr", u.Address()))
				u.conn.Close()
				break
			}
			logger.Info("join success", zap.Any("addr", u.Address()))
		} else {
			var tmp []byte
			for 0 < len(inbuf) {
				size := int(inbuf[0])
				if size == 0 {
					// a zero size frame never advances; the stream cannot be resynced
					floodLog.Error("tcp zero size frame", zap.Any("addr", u.Address()))
					return
				}
				if size <= len(inbuf) {
					tmp = append(tmp, inbuf[:size]...)
					inbuf = inbuf[size:]
				} else {
					break
				}
			}
			if 0 < len(tmp) {
				msg := McsMessage{UserID: u.UserID(), Seq: u.seq, Body: tmp}
				u.seq++
				u.room.SendMessage(u, msg)
			}
		}
	}
}
