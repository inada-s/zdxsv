package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
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

// Battle UDP packets between zproxy and the battle server, and between zproxies.
// They share zproxy's socket with protobuf Ping/Pong; byte 0 is 0, which no
// protobuf message starts with.
const (
	KindHello       byte = 1
	KindHelloAnswer byte = 2
	KindBattle      byte = 3
)

type McsMessage struct {
	UserID string
	Seq    uint32
	Body   []byte
}

type Packet struct {
	Kind      byte
	SessionID string
	OK        bool
	Seq       uint32
	Ack       uint32
	Messages  []McsMessage
}

func IsWirePacket(b []byte) bool {
	return len(b) >= 2 && b[0] == 0
}

func AppendHello(b []byte, sessionID string) []byte {
	b = append(b, 0, KindHello)
	return append(b, sessionID...)
}

func AppendHelloAnswer(b []byte, sessionID string, ok bool) []byte {
	b = append(b, 0, KindHelloAnswer, 0)
	if ok {
		b[len(b)-1] = 1
	}
	return append(b, sessionID...)
}

func AppendBattle(b []byte, seq, ack uint32, msgs []McsMessage) []byte {
	b = append(b, 0, KindBattle)
	b = binary.BigEndian.AppendUint32(b, seq)
	b = binary.BigEndian.AppendUint32(b, ack)
	for _, m := range msgs {
		b = append(b, byte(len(m.UserID)))
		b = append(b, m.UserID...)
		b = binary.BigEndian.AppendUint32(b, m.Seq)
		b = binary.BigEndian.AppendUint16(b, uint16(len(m.Body)))
		b = append(b, m.Body...)
	}
	return b
}

var errShort = errors.New("battle packet: short")

// ParsePacket decodes b. Message bodies are copies.
func ParsePacket(b []byte) (Packet, error) {
	var p Packet
	if !IsWirePacket(b) {
		return p, fmt.Errorf("battle packet: not a battle packet")
	}
	p.Kind = b[1]
	b = b[2:]
	switch p.Kind {
	case KindHello:
		p.SessionID = string(b)
	case KindHelloAnswer:
		if len(b) < 1 {
			return p, errShort
		}
		p.OK = b[0] == 1
		p.SessionID = string(b[1:])
	case KindBattle:
		if len(b) < 8 {
			return p, errShort
		}
		p.Seq = binary.BigEndian.Uint32(b)
		p.Ack = binary.BigEndian.Uint32(b[4:])
		b = b[8:]
		for len(b) > 0 {
			n := int(b[0])
			if len(b) < 1+n+6 {
				return p, errShort
			}
			m := McsMessage{UserID: string(b[1 : 1+n])}
			b = b[1+n:]
			m.Seq = binary.BigEndian.Uint32(b)
			size := int(binary.BigEndian.Uint16(b[4:]))
			b = b[6:]
			if len(b) < size {
				return p, errShort
			}
			m.Body = append([]byte{}, b[:size]...)
			b = b[size:]
			p.Messages = append(p.Messages, m)
		}
	default:
		return p, fmt.Errorf("battle packet: kind %d", p.Kind)
	}
	return p, nil
}

const ringSize = 4096

// sendMax caps the messages of one packet.
const sendMax = 50

// BattleBuffer keeps the messages sent to one peer until the peer acks them.
type BattleBuffer struct {
	mtx   sync.Mutex
	id    string
	ack   uint32 // the last seq received from the peer
	begin uint32 // the first seq not acked yet
	end   uint32 // the next seq to give
	ring  []McsMessage
}

func NewBattleBuffer(id string) *BattleBuffer {
	return &BattleBuffer{id: id, begin: 1, end: 1, ring: make([]McsMessage, ringSize)}
}

func (b *BattleBuffer) SetID(id string) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.id = id
}

func (b *BattleBuffer) ID() string {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return b.id
}

func (b *BattleBuffer) Push(msg McsMessage) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.ring[b.end%ringSize] = msg
	b.end++
}

// AppendPacket appends a battle packet of the unacked messages to dst.
func (b *BattleBuffer) AppendPacket(dst []byte) []byte {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	e := b.end
	if b.begin+sendMax < e {
		e = b.begin + sendMax
	}
	l, r := b.begin%ringSize, e%ringSize
	var msgs []McsMessage
	if l <= r {
		msgs = b.ring[l:r]
	} else {
		msgs = append(append(msgs, b.ring[l:]...), b.ring[:r]...)
	}
	return AppendBattle(dst, e-1, b.ack, msgs)
}

func (b *BattleBuffer) ApplySeqAck(seq, ack uint32) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.begin = ack + 1
	b.ack = seq
}

// MessageFilter numbers the messages a user sends and passes each received
// message once and in order, only from the accepted users.
type MessageFilter struct {
	mtx     sync.Mutex
	seq     uint32
	recvSeq map[string]uint32
}

func NewMessageFilter(acceptIDs []string) *MessageFilter {
	mf := &MessageFilter{seq: 1, recvSeq: map[string]uint32{}}
	for _, id := range acceptIDs {
		mf.recvSeq[id] = 0
	}
	return mf
}

func (m *MessageFilter) GenerateMessage(userID string, data []byte) McsMessage {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	msg := McsMessage{UserID: userID, Seq: m.seq, Body: data}
	m.seq++
	return msg
}

func (m *MessageFilter) Filter(msg McsMessage) bool {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	ack, ok := m.recvSeq[msg.UserID]
	if !ok {
		return false
	}
	if ack == 0 || msg.Seq == ack+1 {
		m.recvSeq[msg.UserID] = msg.Seq
		return true
	}
	return false
}
