package battle

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/golang/glog"
	pb "github.com/golang/protobuf/proto"

	"zdxsv/pkg/proto"
)

type UDPPeer struct {
	BasePeer
	room *Room

	addr    *net.UDPAddr
	conn    *net.UDPConn
	rudp    *proto.BattleBuffer
	filter  *proto.MessageFilter
	chFlush chan struct{}
	chRecv  chan struct{}

	readingMtx sync.Mutex
	reading    []*proto.BattleMessage
	reading2   []*proto.BattleMessage

	closeFunc func()
}

func NewUDPPeer(conn *net.UDPConn, addr *net.UDPAddr, userID string) *UDPPeer {
	return &UDPPeer{
		addr:    addr,
		conn:    conn,
		chFlush: make(chan struct{}, 1),
		chRecv:  make(chan struct{}, 1),
		rudp:    proto.NewBattleBuffer(userID),
		filter:  proto.NewMessageFilter([]string{userID}),
	}
}

func (u *UDPPeer) Close() error {
	if u.closeFunc != nil {
		u.closeFunc()
	}
	return nil
}

func (u *UDPPeer) SetUserID(id string) {
	u.userID = id
	u.rudp.SetID(id)
}

func (u *UDPPeer) Serve(logic *Logic) {
	glog.Infoln("[UDP]", u.Address(), "Serve Start")
	defer glog.Infoln("[UDP]", u.Address(), "Serve End")
	ctx, cancel := context.WithCancel(context.Background())
	u.closeFunc = cancel
	defer cancel()
	pbuf := pb.NewBuffer(nil)

	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	lastRecv := time.Now()
	lastSend := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			timeout := time.Since(lastRecv).Seconds() > 5.0
			if timeout {
				glog.Infoln("udp peer timeout", u.Address())
				return
			}
			if time.Since(lastSend).Seconds() >= 0.016 {
				select {
				case u.chFlush <- struct{}{}:
				default:
				}
			}
		case <-u.chFlush:
			lastSend = time.Now()
			data, seq, ack := u.rudp.GetSendData()
			pkt := proto.GetPacket()
			pkt.Type = proto.MessageType_Battle.Enum()
			pkt.BattleData = data
			pkt.Ack = pb.Uint32(ack)
			pkt.Seq = pb.Uint32(seq)
			pbuf.Reset()
			err := pbuf.Marshal(pkt)
			proto.PutPacket(pkt)
			if err != nil {
				glog.Errorf("Marshal error : %v", err)
				return
			}
			u.conn.WriteTo(pbuf.Bytes(), u.addr)
		case <-u.chRecv:
			lastRecv = time.Now()
			u.readingMtx.Lock()
			u.reading, u.reading2 = u.reading2, u.reading
			u.readingMtx.Unlock()

			for _, msg := range u.reading2 {
				if u.room == nil {
					if len(msg.GetBody()) != 22 {
						glog.Errorln("unexpected length:", msg)
						u.Close()
						break
					}
					value := string(msg.GetBody()[12:22])
					sessionID, err := ParseSessionID(value)
					if err != nil {
						glog.Error(err)
						u.Close()
						break
					}
					glog.Infoln("UDPSessionID:", sessionID)
					room := logic.Join(u, sessionID)
					if room == nil {
						glog.Error("failed to join room")
						u.Close()
						break
					}
					u.room = room
				} else if IsFinData(msg.GetBody()) {
					return
				} else {
					u.room.SendMessage(u, msg)
				}
			}
			u.reading2 = u.reading2[:0]
		}
	}
}

func (u *UDPPeer) OnReceive(pkt *proto.Packet) {
	u.rudp.ApplySeqAck(pkt.GetSeq(), pkt.GetAck())

	u.readingMtx.Lock()
	for _, msg := range pkt.GetBattleData() {
		if u.filter.Filter(msg) {
			u.reading = append(u.reading, msg)
		}
	}
	u.readingMtx.Unlock()

	select {
	case u.chRecv <- struct{}{}:
	default:
	}
}

func (u *UDPPeer) Address() string {
	return u.addr.String()
}

func (u *UDPPeer) AddSendData(data []byte) {
	glog.Fatalln("AddSendData called", data)
}

func (u *UDPPeer) AddSendMessage(msg *proto.BattleMessage) {
	u.rudp.PushBattleMessage(msg)
	select {
	case u.chFlush <- struct{}{}:
	default:
	}
}
