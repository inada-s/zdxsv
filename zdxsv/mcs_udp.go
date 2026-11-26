package main

import (
	"context"
	"crypto/rand"
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
	peers       map[string]*McsUDPPeer
	pending     map[string]*udpPending
	hellos      ipLimiter
	joinTimeout time.Duration

	conn *net.UDPConn
	mcs  *Mcs
}

func NewUDPServer(mcs *Mcs) *McsUDPServer {
	return &McsUDPServer{
		peers:       make(map[string]*McsUDPPeer),
		pending:     make(map[string]*udpPending),
		mcs:         mcs,
		hellos:      ipLimiter{max: udpHellosPerIP},
		joinTimeout: udpJoinTimeout,
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
			floodLog.Error("udp read failed", zap.Error(err))
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
			floodLog.Error("bad udp packet", zap.Any("addr", key), zap.Error(err), zap.Int("bytes", n), zap.Binary("head", logHead(buf[:n])))
			continue
		}

		switch pkt.Kind {
		case KindHello:
			if !s.hellos.allow(addr.IP, time.Now()) {
				floodLog.Warn("udp hello limit", zap.Any("addr", key))
				continue
			}
			// The client repeats its hello until it gets an answer.
			user, valid := s.mcs.FindWaitingUser(pkt.SessionID)
			if !found && valid {
				s.addPending(key, user.UserID)
			}
			s.conn.WriteToUDP(AppendHelloAnswer(nil, pkt.SessionID, valid), addr)
		case KindBattle:
			if found {
				peer.OnReceive(pkt)
				continue
			}
			s.onPendingBattle(key, addr, pkt)
		default:
			floodLog.Error("received unexpected packet kind", zap.Any("addr", key), zap.Uint8("kind", pkt.Kind))
		}
	}
}

const (
	udpPendingMax = 1024
	udpPendingTTL = 10 * time.Second
	udpPeerMax    = 4 * MaxRoom
	// A reconnecting client has a new peer before the old one times out.
	udpPeersPerUser = 2
	// zproxy sends up to 10 hellos per battle; players can share an IP.
	udpHellosPerIP = 40
	udpJoinTimeout = 10 * time.Second
)

// udpPending is an address that sent a valid hello. It becomes a peer once it
// acks the cookie, sent to it as the seq of an empty battle packet.
type udpPending struct {
	userID  string
	cookie  uint32
	expires time.Time
}

func newCookie() uint32 {
	var b [4]byte
	rand.Read(b[:])
	// Below 1<<31 so that the peer's seq does not wrap.
	return binary.BigEndian.Uint32(b[:])>>1 | 1
}

func (s *McsUDPServer) addPending(key, userID string) {
	s.Lock()
	defer s.Unlock()
	now := time.Now()
	if p, ok := s.pending[key]; ok && p.userID == userID {
		p.expires = now.Add(udpPendingTTL)
		return
	}
	if len(s.pending) >= udpPendingMax {
		for k, p := range s.pending {
			if now.After(p.expires) {
				delete(s.pending, k)
			}
		}
	}
	if len(s.pending) >= udpPendingMax {
		floodLog.Warn("udp pending full", zap.Any("addr", key))
		return
	}
	s.pending[key] = &udpPending{userID: userID, cookie: newCookie(), expires: now.Add(udpPendingTTL)}
}

func (s *McsUDPServer) onPendingBattle(key string, addr *net.UDPAddr, pkt Packet) {
	s.Lock()
	p, ok := s.pending[key]
	if ok && time.Now().After(p.expires) {
		delete(s.pending, key)
		ok = false
	}
	if !ok {
		s.Unlock()
		floodLog.Error("battle data received but peer not found", zap.Any("addr", key))
		return
	}
	if pkt.Ack != p.cookie {
		s.Unlock()
		s.conn.WriteToUDP(AppendBattle(nil, p.cookie, 0, nil), addr)
		return
	}
	if len(s.peers) >= udpPeerMax {
		s.Unlock()
		floodLog.Warn("udp peers full", zap.Any("addr", key))
		return
	}
	if s.userPeers(p.userID) >= udpPeersPerUser {
		s.Unlock()
		floodLog.Warn("udp peers of user full", zap.Any("addr", key))
		return
	}
	delete(s.pending, key)
	peer := NewUDPPeer(s.conn, addr, p.userID, p.cookie)
	peer.joinTimeout = s.joinTimeout
	s.peers[key] = peer
	s.Unlock()

	logger.Info("join udp peer", zap.Any("addr", key))
	go func() {
		peer.Serve(s.mcs)
		logger.Info("leave udp peer", zap.Any("addr", key))
		if peer.room != nil {
			peer.room.Leave(peer)
		}
		s.Lock()
		delete(s.peers, key)
		s.Unlock()
	}()
	peer.OnReceive(pkt)
}

func (s *McsUDPServer) userPeers(userID string) int {
	n := 0
	for _, p := range s.peers {
		if p.rudp.ID() == userID {
			n++
		}
	}
	return n
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

	addr        *net.UDPAddr
	conn        *net.UDPConn
	rudp        *BattleBuffer
	filter      *MessageFilter
	chFlush     chan struct{}
	chRecv      chan struct{}
	joinTimeout time.Duration

	readingMtx sync.Mutex
	reading    []McsMessage
	reading2   []McsMessage

	closeFunc func()
}

func NewUDPPeer(conn *net.UDPConn, addr *net.UDPAddr, userID string, firstSeq uint32) *McsUDPPeer {
	return &McsUDPPeer{
		addr:        addr,
		conn:        conn,
		chFlush:     make(chan struct{}, 1),
		chRecv:      make(chan struct{}, 1),
		rudp:        newBattleBufferAt(userID, firstSeq),
		filter:      NewMessageFilter([]string{userID}),
		joinTimeout: udpJoinTimeout,
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
	start := time.Now()
	lastRecv := start
	lastSend := start

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
			if u.room == nil && time.Since(start) > u.joinTimeout {
				floodLog.Info("udp peer did not join", zap.Any("addr", u.Address()))
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
						floodLog.Error("unexpected length", zap.Any("msg", msg))
						u.Close()
						break
					}
					value := string(msg.Body[12:22])
					sessionID, err := ParseSessionID(value)
					if err != nil {
						floodLog.Error("udp session id", zap.Error(err))
						u.Close()
						break
					}
					logger.Info("udp session id", zap.Any("session_id", sessionID))
					room := mcs.Join(u, sessionID)
					if room == nil {
						floodLog.Error("failed to join room")
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
// They share zproxy's socket with JSON Ping/Pong; byte 0 is 0, never '{'.
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

// newBattleBufferAt starts the seq after seq: the peer acked seq already.
func newBattleBufferAt(id string, seq uint32) *BattleBuffer {
	b := NewBattleBuffer(id)
	b.begin, b.end = seq+1, seq+1
	return b
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
	b.ack = seq
	// Only acks of sent messages, wrap included: a later ack would make the
	// ring window wrap and send thousands of stale messages.
	if ack+1-b.begin <= b.end-b.begin {
		b.begin = ack + 1
	}
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
