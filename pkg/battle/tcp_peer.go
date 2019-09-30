package battle

import (
	"encoding/hex"
	"io"
	"net"
	"sync"
	"time"

	"github.com/golang/glog"
	pb "github.com/golang/protobuf/proto"

	"zdxsv/pkg/proto"
)

type TCPPeer struct {
	BasePeer

	sendMtx sync.Mutex
	conn    *net.TCPConn
	room    *Room
	seq     uint32
}

func NewTCPPeer(conn *net.TCPConn) *TCPPeer {
	return &TCPPeer{
		conn: conn,
		seq:  1,
	}
}

func (u *TCPPeer) Close() error {
	return u.conn.Close()
}

func (u *TCPPeer) Serve(logic *Logic) {
	glog.Infoln("[TCP]", u.Address(), "Serve Start")
	defer glog.Infoln("[TCP]", u.Address(), "Serve End")
	data, _ := hex.DecodeString("280110310000000100ffffff")
	u.AddSendData(data)
	u.readLoop(logic)
	if u.room != nil {
		u.room.Leave(u)
		u.room = nil
	}
	u.conn.Close()
}

func (u *TCPPeer) AddSendMessage(msg *proto.BattleMessage) {
	u.AddSendData(msg.GetBody())
}

func (u *TCPPeer) AddSendData(data []byte) {
	u.sendMtx.Lock()
	defer u.sendMtx.Unlock()
	for sum := 0; sum < len(data); {
		n, err := u.conn.Write(data[sum:])
		if err != nil {
			glog.Errorf("%v write error: %v\n", u.Address(), err)
			break
		}
		sum += n
	}
}

func (u *TCPPeer) Address() string {
	return u.conn.RemoteAddr().String()
}

func (u *TCPPeer) readLoop(logic *Logic) {
	buf := make([]byte, 1024)
	inbuf := make([]byte, 0, 128)

	for {
		u.conn.SetReadDeadline(time.Now().Add(time.Second * 10))
		n, err := u.conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				glog.Errorf("%v read error: %v\n", u.Address(), err)
			}
			return
		}
		if IsFinData(buf) {
			return
		}
		inbuf = append(inbuf, buf[:n]...)

		if u.room == nil {
			glog.Infoln("room nil: ", u.Address())
			if len(inbuf) < 22 {
				continue
			}
			value := string(inbuf[12:22])
			inbuf = inbuf[:0]
			sessionId, err := ParseSessionId(value)
			glog.Infoln("[TCP] SessionId", sessionId, err)
			u.room = logic.Join(u, sessionId)
			if u.room == nil {
				glog.Infoln("failed to join room: ", u.UserId(), u.Address())
				u.conn.Close()
				break
			}
			glog.Infoln("join success", u.Address())
		} else {
			var tmp []byte
			for 0 < len(inbuf) {
				size := int(inbuf[0])
				if size <= len(inbuf) {
					tmp = append(tmp, inbuf[:size]...)
					inbuf = inbuf[size:]
				} else {
					break
				}
			}
			if 0 < len(tmp) {
				msg := proto.GetBattleMessage()
				msg.Body = tmp
				msg.UserId = pb.String(u.UserId())
				msg.Seq = pb.Uint32(u.seq)
				u.seq++
				u.room.SendMessage(u, msg)
			}
		}
	}
}
