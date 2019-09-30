package battle

import (
	"net"
	"sync"

	"github.com/golang/glog"
	pb "github.com/golang/protobuf/proto"

	"zdxsv/pkg/proto"
)

type UDPServer struct {
	sync.Mutex
	peers map[string]*UDPPeer

	conn  *net.UDPConn
	logic *Logic
}

func NewUDPServer(logic *Logic) *UDPServer {
	return &UDPServer{
		peers: make(map[string]*UDPPeer),
		logic: logic,
	}
}

func (s *UDPServer) readLoop() error {
	glog.Infoln("UDP readLoop()")
	defer glog.Infoln("UDP readLoop() return")
	pkt := proto.GetPacket()
	buf := make([]byte, 4096)
	pbuf := pb.NewBuffer(nil)

	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			glog.Errorln(err)
			continue
		}
		if n == 0 {
			continue
		}

		key := addr.String()
		s.Lock()
		peer, found := s.peers[key]
		s.Unlock()

		pkt.Reset()
		pbuf.SetBuf(buf[:n])
		if err := pbuf.Unmarshal(pkt); err != nil {
			glog.Errorf("failed to unmarshal udp packet from %v data %v", key, buf[:n])
			continue
		}

		switch pkt.GetType() {
		case proto.MessageType_HelloServer:
			// 対戦前に正しいUDPパケットを送ってきたクライアントのみ受け付ける
			// セッションIDが正しいかの検証を行う
			// UDPのためクライアントは複数回このリクエストを送ってくる
			sessionId := pkt.GetHelloServerData().GetSessionId()
			user, valid := s.logic.FindWaitingUser(sessionId)
			if !found && valid {
				glog.Infoln("join udp peer", key)
				peer := NewUDPPeer(s.conn, addr, user.UserId)

				s.Lock()
				s.peers[key] = peer
				s.Unlock()

				go func(key string) {
					peer.Serve(s.logic)
					glog.Infoln("leave udp peer", key)
					if peer.room != nil {
						peer.room.Leave(peer)
					}
					s.Lock()
					delete(s.peers, key)
					s.Unlock()
				}(key)
			}
			pkt.Reset()
			pkt.Type = proto.MessageType_HelloServer.Enum()
			pkt.HelloServerData = &proto.HelloServerMessage{
				Ok:        pb.Bool(valid),
				SessionId: pb.String(sessionId),
			}
			if data, err := pb.Marshal(pkt); err != nil {
				glog.Errorln(err)
			} else {
				s.conn.WriteToUDP(data, addr)
			}
		case proto.MessageType_Battle:
			if !found {
				glog.Errorln("battle data received but peer not found", pkt)
				continue
			}
			peer.OnReceive(pkt)
		default:
			glog.Errorf("received unexpected pkt type packet %v", pkt)
		}
	}
	return nil
}

func (s *UDPServer) ListenAndServe(addr string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	s.conn = conn
	s.conn.SetReadBuffer(16 * 1024 * 1024)
	s.conn.SetWriteBuffer(16 * 1024 * 1024)
	s.readLoop()
	return nil
}
