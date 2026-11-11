package battle

import (
	"encoding/hex"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
)

type McsTCPServer struct {
	logic *Mcs
}

func NewTCPServer(logic *Mcs) *McsTCPServer {
	return &McsTCPServer{
		logic: logic,
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
		conn, err := l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return err
		}
		if err != nil {
			continue
		}
		peer := NewTCPPeer(conn)
		go peer.Serve(s.logic)
	}
}

type McsTCPPeer struct {
	BaseMcsPeer

	sendMtx sync.Mutex
	conn    net.Conn
	room    *McsRoom
	seq     uint32
}

func NewTCPPeer(conn net.Conn) *McsTCPPeer {
	return &McsTCPPeer{
		conn: conn,
		seq:  1,
	}
}

func (u *McsTCPPeer) Close() error {
	return u.conn.Close()
}

func (u *McsTCPPeer) Serve(logic *Mcs) {
	zlog.Info("tcp serve start", zap.Any("addr", u.Address()))
	defer zlog.Info("tcp serve end", zap.Any("addr", u.Address()))
	data, _ := hex.DecodeString("280110310000000100ffffff")
	u.AddSendData(data)
	u.readLoop(logic)
	if u.room != nil {
		u.room.Leave(u)
		u.room = nil
	}
	u.conn.Close()
}

func (u *McsTCPPeer) AddSendMessage(msg McsMessage) {
	u.AddSendData(msg.Body)
}

func (u *McsTCPPeer) AddSendData(data []byte) {
	u.sendMtx.Lock()
	defer u.sendMtx.Unlock()
	for sum := 0; sum < len(data); {
		n, err := u.conn.Write(data[sum:])
		if err != nil {
			zlog.Error("tcp write error", zap.Any("addr", u.Address()), zap.Error(err))
			break
		}
		sum += n
	}
}

func (u *McsTCPPeer) Address() string {
	return u.conn.RemoteAddr().String()
}

func (u *McsTCPPeer) readLoop(logic *Mcs) {
	buf := make([]byte, 1024)
	inbuf := make([]byte, 0, 128)

	for {
		u.conn.SetReadDeadline(time.Now().Add(time.Second * 10))
		n, err := u.conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				zlog.Error("tcp read error", zap.Any("addr", u.Address()), zap.Error(err))
			}
			return
		}
		if IsFinData(buf[:n]) {
			return
		}
		inbuf = append(inbuf, buf[:n]...)

		if u.room == nil {
			zlog.Info("room nil", zap.Any("addr", u.Address()))
			if len(inbuf) < 22 {
				continue
			}
			value := string(inbuf[12:22])
			inbuf = inbuf[:0]
			sessionID, err := ParseSessionID(value)
			zlog.Info("tcp session id", zap.Any("session_id", sessionID), zap.Error(err))
			u.room = logic.Join(u, sessionID)
			if u.room == nil {
				zlog.Info("failed to join room", zap.Any("user_id", u.UserID()), zap.Any("addr", u.Address()))
				u.conn.Close()
				break
			}
			zlog.Info("join success", zap.Any("addr", u.Address()))
		} else {
			var tmp []byte
			for 0 < len(inbuf) {
				size := int(inbuf[0])
				if size == 0 {
					// a zero size frame never advances; the stream cannot be resynced
					zlog.Error("tcp zero size frame", zap.Any("addr", u.Address()))
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
