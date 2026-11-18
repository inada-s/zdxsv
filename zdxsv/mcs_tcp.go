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
	mcs *Mcs
}

func NewTCPServer(mcs *Mcs) *McsTCPServer {
	return &McsTCPServer{
		mcs: mcs,
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
		peer := NewTCPPeer(conn)
		go peer.Serve(s.mcs)
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
	tcpSendQueueLen = 1024
	tcpWriteTimeout = 10 * time.Second
)

func NewTCPPeer(conn net.Conn) *McsTCPPeer {
	u := &McsTCPPeer{
		conn:  conn,
		seq:   1,
		sendq: make(chan []byte, tcpSendQueueLen),
		done:  make(chan struct{}),
	}
	go u.writeLoop()
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
	logger.Info("tcp serve start", zap.Any("addr", u.Address()))
	defer logger.Info("tcp serve end", zap.Any("addr", u.Address()))
	data, _ := hex.DecodeString("280110310000000100ffffff")
	u.AddSendData(data)
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
		logger.Error("tcp send queue full", zap.Any("addr", u.Address()))
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
				logger.Error("tcp write error", zap.Any("addr", u.Address()), zap.Error(err))
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

	for {
		u.conn.SetReadDeadline(time.Now().Add(time.Second * 10))
		n, err := u.conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				logger.Error("tcp read error", zap.Any("addr", u.Address()), zap.Error(err))
			}
			return
		}
		if IsFinData(buf[:n]) {
			return
		}
		inbuf = append(inbuf, buf[:n]...)

		if u.room == nil {
			logger.Info("room nil", zap.Any("addr", u.Address()))
			if len(inbuf) < 22 {
				continue
			}
			value := string(inbuf[12:22])
			inbuf = inbuf[:0]
			sessionID, err := ParseSessionID(value)
			logger.Info("tcp session id", zap.Any("session_id", sessionID), zap.Error(err))
			u.room = mcs.Join(u, sessionID)
			if u.room == nil {
				logger.Info("failed to join room", zap.Any("user_id", u.UserID()), zap.Any("addr", u.Address()))
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
					logger.Error("tcp zero size frame", zap.Any("addr", u.Address()))
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
