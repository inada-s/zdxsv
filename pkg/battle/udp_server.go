package battle

import (
	"errors"
	"net"
	"sync"

	pb "github.com/golang/protobuf/proto"
	"go.uber.org/zap"
	"zdxsv/pkg/zlog"

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
	zlog.Info("udp readLoop start")
	defer zlog.Info("udp readLoop end")
	pkt := proto.GetPacket()
	buf := make([]byte, 4096)
	pbuf := pb.NewBuffer(nil)

	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if errors.Is(err, net.ErrClosed) {
			return err
		}
		if err != nil {
			zlog.Error("udp read failed", zap.Error(err))
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
			zlog.Error("failed to unmarshal udp packet", zap.Any("addr", key), zap.Binary("data", buf[:n]))
			continue
		}

		switch pkt.GetType() {
		case proto.MessageType_HelloServer:
			// 対戦前に正しいUDPパケットを送ってきたクライアントのみ受け付ける
			// セッションIDが正しいかの検証を行う
			// UDPのためクライアントは複数回このリクエストを送ってくる
			sessionID := pkt.GetHelloServerData().GetSessionId()
			user, valid := s.logic.FindWaitingUser(sessionID)
			if !found && valid {
				zlog.Info("join udp peer", zap.Any("addr", key))
				peer := NewUDPPeer(s.conn, addr, user.UserID)

				s.Lock()
				s.peers[key] = peer
				s.Unlock()

				go func(key string) {
					peer.Serve(s.logic)
					zlog.Info("leave udp peer", zap.Any("addr", key))
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
				SessionId: pb.String(sessionID),
			}
			if data, err := pb.Marshal(pkt); err != nil {
				zlog.Error("udp packet failure", zap.Error(err))
			} else {
				s.conn.WriteToUDP(data, addr)
			}
		case proto.MessageType_Battle:
			if !found {
				zlog.Error("battle data received but peer not found", zap.Any("pkt", pkt))
				continue
			}
			peer.OnReceive(pkt)
		default:
			zlog.Error("received unexpected packet type", zap.Any("pkt", pkt))
		}
	}
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
	return s.Serve(conn)
}

// Serve handles packets on conn until it is closed.
func (s *UDPServer) Serve(conn *net.UDPConn) error {
	s.conn = conn
	s.conn.SetReadBuffer(16 * 1024 * 1024)
	s.conn.SetWriteBuffer(16 * 1024 * 1024)
	return s.readLoop()
}
