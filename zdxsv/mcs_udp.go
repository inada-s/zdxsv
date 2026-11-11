package main

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

type McsUDPServer struct {
	sync.Mutex
	peers map[string]*McsUDPPeer

	conn *net.UDPConn
	mcs  *Mcs
}

func NewUDPServer(mcs *Mcs) *McsUDPServer {
	return &McsUDPServer{
		peers: make(map[string]*McsUDPPeer),
		mcs:   mcs,
	}
}

func (s *McsUDPServer) readLoop() error {
	logger.Info("udp readLoop start")
	defer logger.Info("udp readLoop end")
	buf := make([]byte, 4096)

	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if errors.Is(err, net.ErrClosed) {
			return err
		}
		if err != nil {
			logger.Error("udp read failed", zap.Error(err))
			continue
		}
		if n == 0 {
			continue
		}

		key := addr.String()
		s.Lock()
		peer, found := s.peers[key]
		s.Unlock()

		pkt, err := ParsePacket(buf[:n])
		if err != nil {
			logger.Error("bad udp packet", zap.Any("addr", key), zap.Error(err), zap.Binary("data", buf[:n]))
			continue
		}

		switch pkt.Kind {
		case KindHello:
			// 対戦前に正しいUDPパケットを送ってきたクライアントのみ受け付ける
			// セッションIDが正しいかの検証を行う
			// UDPのためクライアントは複数回このリクエストを送ってくる
			user, valid := s.mcs.FindWaitingUser(pkt.SessionID)
			if !found && valid {
				logger.Info("join udp peer", zap.Any("addr", key))
				peer := NewUDPPeer(s.conn, addr, user.UserID)

				s.Lock()
				s.peers[key] = peer
				s.Unlock()

				go func(key string) {
					peer.Serve(s.mcs)
					logger.Info("leave udp peer", zap.Any("addr", key))
					if peer.room != nil {
						peer.room.Leave(peer)
					}
					s.Lock()
					delete(s.peers, key)
					s.Unlock()
				}(key)
			}
			s.conn.WriteToUDP(AppendHelloAnswer(nil, pkt.SessionID, valid), addr)
		case KindBattle:
			if !found {
				logger.Error("battle data received but peer not found", zap.Any("addr", key))
				continue
			}
			peer.OnReceive(pkt)
		default:
			logger.Error("received unexpected packet kind", zap.Any("addr", key), zap.Uint8("kind", pkt.Kind))
		}
	}
}

func (s *McsUDPServer) ListenAndServe(addr string) error {
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
func (s *McsUDPServer) Serve(conn *net.UDPConn) error {
	s.conn = conn
	s.conn.SetReadBuffer(16 * 1024 * 1024)
	s.conn.SetWriteBuffer(16 * 1024 * 1024)
	return s.readLoop()
}

type McsUDPPeer struct {
	BaseMcsPeer
	room *McsRoom

	addr    *net.UDPAddr
	conn    *net.UDPConn
	rudp    *BattleBuffer
	filter  *MessageFilter
	chFlush chan struct{}
	chRecv  chan struct{}

	readingMtx sync.Mutex
	reading    []McsMessage
	reading2   []McsMessage

	closeFunc func()
}

func NewUDPPeer(conn *net.UDPConn, addr *net.UDPAddr, userID string) *McsUDPPeer {
	return &McsUDPPeer{
		addr:    addr,
		conn:    conn,
		chFlush: make(chan struct{}, 1),
		chRecv:  make(chan struct{}, 1),
		rudp:    NewBattleBuffer(userID),
		filter:  NewMessageFilter([]string{userID}),
	}
}

func (u *McsUDPPeer) Close() error {
	if u.closeFunc != nil {
		u.closeFunc()
	}
	return nil
}

func (u *McsUDPPeer) SetUserID(id string) {
	u.userID = id
	u.rudp.SetID(id)
}

func (u *McsUDPPeer) Serve(mcs *Mcs) {
	logger.Info("udp serve start", zap.Any("addr", u.Address()))
	defer logger.Info("udp serve end", zap.Any("addr", u.Address()))
	ctx, cancel := context.WithCancel(context.Background())
	u.closeFunc = cancel
	defer cancel()
	var sendBuf []byte

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
				logger.Info("udp peer timeout", zap.Any("addr", u.Address()))
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
			sendBuf = u.rudp.AppendPacket(sendBuf[:0])
			u.conn.WriteTo(sendBuf, u.addr)
		case <-u.chRecv:
			lastRecv = time.Now()
			u.readingMtx.Lock()
			u.reading, u.reading2 = u.reading2, u.reading
			u.readingMtx.Unlock()

			for _, msg := range u.reading2 {
				if u.room == nil {
					if len(msg.Body) != 22 {
						logger.Error("unexpected length", zap.Any("msg", msg))
						u.Close()
						break
					}
					value := string(msg.Body[12:22])
					sessionID, err := ParseSessionID(value)
					if err != nil {
						logger.Error("udp session id", zap.Error(err))
						u.Close()
						break
					}
					logger.Info("udp session id", zap.Any("session_id", sessionID))
					room := mcs.Join(u, sessionID)
					if room == nil {
						logger.Error("failed to join room")
						u.Close()
						break
					}
					u.room = room
				} else if IsFinData(msg.Body) {
					return
				} else {
					u.room.SendMessage(u, msg)
				}
			}
			u.reading2 = u.reading2[:0]
		}
	}
}

func (u *McsUDPPeer) OnReceive(pkt Packet) {
	u.rudp.ApplySeqAck(pkt.Seq, pkt.Ack)

	u.readingMtx.Lock()
	for _, msg := range pkt.Messages {
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

func (u *McsUDPPeer) Address() string {
	return u.addr.String()
}

func (u *McsUDPPeer) AddSendData(data []byte) {
	logger.Fatal("AddSendData called", zap.Binary("data", data))
}

func (u *McsUDPPeer) AddSendMessage(msg McsMessage) {
	u.rudp.Push(msg)
	select {
	case u.chFlush <- struct{}{}:
	default:
	}
}
