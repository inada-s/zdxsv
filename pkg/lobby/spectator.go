package lobby

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"sync"
	"time"

	"zdxsv/pkg/proto"

	"github.com/golang/glog"
	pb "github.com/golang/protobuf/proto"
)

// Live spectating over the lobby's UDP socket (ServeUDPStunServer), as gdxsv's
// lbs_spectator.go: one GGPO participant of a lobby battle (live_uplink=1 in the
// battle info) pushes its replay as it records it, and the lobby fans it out to
// spectators that subscribed with a cookie proving their address.
//
// A pcsx2 replay starts from a save state (~8 MB), so unlike gdxsv every stream
// (header, start state, inputs) is go-back-N: up to liveWindow datagrams past the
// receiver's ack, back to the ack after liveResend without progress. Receivers
// keep contiguous data only and ack every push. Order to a spectator: header,
// state, inputs, close.
const (
	liveChunkBytes          = 1000
	liveWindow              = 32
	liveResend              = 200 * time.Millisecond
	liveMaxFramesPerPush    = 128
	liveMaxHeaderBytes      = 16 << 10
	liveMaxStateBytes       = 64 << 20
	liveMaxFrameBytes       = 4 * 64
	liveFanoutInterval      = 50 * time.Millisecond
	liveSubscriberTimeout   = 10 * time.Second
	liveRetention           = 10 * time.Minute // after close
	liveIdle                = 10 * time.Minute // without a push
	liveMaxSubscribers      = 4096
	liveMaxBattleSubscriber = 512
	liveCookieBytes         = 16
	liveCookieWindow        = 30 * time.Second
)

// Spectators is the lobby's registry; battleInfoNotice opens a session per GGPO battle.
var Spectators = NewSpectatorRegistry()

type SpectatorRegistry struct {
	mu        sync.Mutex
	cookieKey [32]byte
	sessions  map[string]*liveSession
	nsubs     int
	send      func(data []byte, to *net.UDPAddr)
}

type liveSession struct {
	code        string
	session     int32
	opened      time.Time
	lastPush    time.Time
	closed      time.Time // zero while running
	publisher   *net.UDPAddr
	header      []byte
	state       []byte
	stateTotal  int
	frameBytes  int
	inputs      []byte
	closeReason string
	subs        map[string]*liveSubscriber
}

// liveStream is a receiver's go-back-N position: bytes of the state, or frames.
type liveStream struct {
	acked, next int
	progress    time.Time
}

type liveSubscriber struct {
	addr       *net.UDPAddr
	seen       time.Time
	headerAck  bool
	headerSent time.Time
	state      liveStream
	frames     liveStream
	closeAck   bool
	closeSent  time.Time
}

func NewSpectatorRegistry() *SpectatorRegistry {
	r := &SpectatorRegistry{sessions: map[string]*liveSession{}}
	if _, err := rand.Read(r.cookieKey[:]); err != nil {
		glog.Fatalln(err)
	}
	return r
}

// Open starts accepting the battle's uplink (session = its ggpo_session). Idempotent.
func (r *SpectatorRegistry) Open(code string, session uint32, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[code]; ok {
		return
	}
	r.sessions[code] = &liveSession{code: code, session: int32(session), opened: now, lastPush: now, subs: map[string]*liveSubscriber{}}
	glog.Infoln("live open", code)
}

// Serve sends with conn and runs the fanout and sweep until conn is closed.
func (r *SpectatorRegistry) Serve(conn *net.UDPConn) {
	r.mu.Lock()
	r.send = func(data []byte, to *net.UDPAddr) { conn.WriteToUDP(data, to) }
	r.mu.Unlock()
	fanout := time.NewTicker(liveFanoutInterval)
	defer fanout.Stop()
	sweep := time.Now()
	for now := range fanout.C {
		r.Fanout(now)
		if now.Sub(sweep) >= time.Minute {
			r.Sweep(now)
			sweep = now
		}
	}
}

// Handle takes a spectator packet (false: not one).
func (r *SpectatorRegistry) Handle(pkt *proto.Packet, from *net.UDPAddr, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch pkt.GetType() {
	case proto.MessageType_SpectatorInputPushType:
		r.onPush(pkt.GetSpectatorInputPushData(), from, now)
	case proto.MessageType_SpectatorInputAckType:
		r.onAck(pkt.GetSpectatorInputAckData(), from, now)
	case proto.MessageType_SpectatorSubscribeType:
		r.onSubscribe(pkt.GetSpectatorSubscribeData(), from, now)
	default:
		return false
	}
	return true
}

func (r *SpectatorRegistry) sendPacket(pkt *proto.Packet, to *net.UDPAddr) {
	if r.send == nil {
		return
	}
	data, err := pb.Marshal(pkt)
	if err != nil {
		glog.Errorln(err)
		return
	}
	r.send(data, to)
}

func (r *SpectatorRegistry) onPush(m *proto.SpectatorInputPush, from *net.UDPAddr, now time.Time) {
	s := r.sessions[m.GetBattleCode()]
	if m == nil || s == nil || m.GetSessionId() != s.session {
		return
	}
	if s.publisher == nil {
		s.publisher = from
		glog.Infoln("live uplink", s.code, from)
	}
	if !sameAddr(s.publisher, from) {
		return
	}
	s.lastPush = now
	if h := m.GetHeader(); len(h) > 0 && s.header == nil && len(h) <= liveMaxHeaderBytes {
		s.header = append([]byte(nil), h...)
	}
	if t := int(m.GetStateTotal()); 0 < t && t <= liveMaxStateBytes && (s.stateTotal == 0 || s.stateTotal == t) {
		s.stateTotal = t
		if st := m.GetState(); int(m.GetStateOffset()) <= len(s.state) {
			end := int(m.GetStateOffset()) + len(st)
			if len(s.state) < end && end <= t {
				s.state = append(s.state, st[len(s.state)-int(m.GetStateOffset()):]...)
			}
		}
	}
	if fb := int(m.GetFrameBytes()); 0 < fb && fb <= liveMaxFrameBytes && (s.frameBytes == 0 || s.frameBytes == fb) {
		s.frameBytes = fb
		in := m.GetInputData()
		start := int(m.GetStartFrame()) * fb
		if len(in)%fb == 0 && 0 <= start && start <= len(s.inputs) && len(s.inputs) < start+len(in) {
			s.inputs = append(s.inputs, in[len(s.inputs)-start:]...)
		}
	}
	if m.CloseReason != nil && s.closed.IsZero() && s.frameBytes > 0 && int(m.GetStartFrame())*s.frameBytes == len(s.inputs) {
		s.closed = now
		s.closeReason = m.GetCloseReason()
		glog.Infoln("live close", s.code, s.closeReason, "frames", len(s.inputs)/s.frameBytes)
	}
	r.sendPacket(&proto.Packet{
		Type: proto.MessageType_SpectatorInputAckType.Enum(),
		SpectatorInputAckData: &proto.SpectatorInputAck{
			BattleCode: pb.String(s.code),
			AckFrame:   pb.Int32(int32(s.frames())),
			HeaderAck:  pb.Bool(s.header != nil),
			StateAck:   pb.Int32(int32(len(s.state))),
			CloseAck:   pb.Bool(!s.closed.IsZero()),
		},
	}, from)
}

func (s *liveSession) frames() int {
	if s.frameBytes == 0 {
		return 0
	}
	return len(s.inputs) / s.frameBytes
}

func (r *SpectatorRegistry) onAck(m *proto.SpectatorInputAck, from *net.UDPAddr, now time.Time) {
	s := r.sessions[m.GetBattleCode()]
	if s == nil {
		return
	}
	sub := s.subs[from.String()]
	if sub == nil {
		return
	}
	sub.headerAck = sub.headerAck || m.GetHeaderAck()
	sub.state.ack(int(m.GetStateAck()), now)
	sub.frames.ack(int(m.GetAckFrame()), now)
	sub.closeAck = sub.closeAck || m.GetCloseAck()
	r.fanoutTo(s, sub, now)
}

func (st *liveStream) ack(n int, now time.Time) {
	if st.acked < n {
		st.acked = n
		st.progress = now
	}
	if st.next < st.acked {
		st.next = st.acked
	}
}

func (r *SpectatorRegistry) onSubscribe(m *proto.SpectatorSubscribeRequest, from *net.UDPAddr, now time.Time) {
	code := m.GetBattleCode()
	if code == "" {
		code = r.newest()
	}
	s := r.sessions[code]
	if s == nil {
		return
	}
	if !r.validCookie(code, from, m.GetCookie(), now) {
		r.sendPacket(&proto.Packet{
			Type: proto.MessageType_SpectatorSubscribeChallengeType.Enum(),
			SpectatorSubscribeChallengeData: &proto.SpectatorSubscribeChallenge{
				BattleCode: pb.String(code),
				Cookie:     r.cookie(code, from, now.Unix()/int64(liveCookieWindow/time.Second)),
			},
		}, from)
		return
	}
	key := from.String()
	sub := s.subs[key]
	if sub == nil {
		if r.nsubs >= liveMaxSubscribers || len(s.subs) >= liveMaxBattleSubscriber {
			return
		}
		sub = &liveSubscriber{addr: from, state: liveStream{progress: now}, frames: liveStream{progress: now}}
		s.subs[key] = sub
		r.nsubs++
		glog.Infoln("live subscribe", code, from)
	}
	sub.seen = now
	r.fanoutTo(s, sub, now)
}

// newest returns the latest opened battle with an uplink, preferring running ones.
func (r *SpectatorRegistry) newest() string {
	var best *liveSession
	for _, s := range r.sessions {
		if s.publisher == nil {
			continue
		}
		if best == nil || (best.closed.IsZero() == s.closed.IsZero() && best.opened.Before(s.opened)) || (!best.closed.IsZero() && s.closed.IsZero()) {
			best = s
		}
	}
	if best == nil {
		return ""
	}
	return best.code
}

// Fanout sends every subscriber what its window allows and drops silent ones.
func (r *SpectatorRegistry) Fanout(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		for key, sub := range s.subs {
			if now.Sub(sub.seen) > liveSubscriberTimeout {
				delete(s.subs, key)
				r.nsubs--
				continue
			}
			r.fanoutTo(s, sub, now)
		}
	}
}

func (r *SpectatorRegistry) fanoutTo(s *liveSession, sub *liveSubscriber, now time.Time) {
	push := func(m *proto.SpectatorInputPush) {
		m.BattleCode = pb.String(s.code)
		r.sendPacket(&proto.Packet{Type: proto.MessageType_SpectatorInputPushType.Enum(), SpectatorInputPushData: m}, sub.addr)
	}
	if !sub.headerAck {
		if s.header != nil && now.Sub(sub.headerSent) >= liveResend {
			sub.headerSent = now
			push(&proto.SpectatorInputPush{Header: s.header})
		}
		return
	}
	if s.stateTotal == 0 || sub.state.acked < s.stateTotal {
		sub.state.window(now, len(s.state), liveChunkBytes, func(off, n int) {
			push(&proto.SpectatorInputPush{State: s.state[off : off+n], StateOffset: pb.Int32(int32(off)), StateTotal: pb.Int32(int32(s.stateTotal))})
		})
		return
	}
	if fb := s.frameBytes; fb > 0 {
		per := liveChunkBytes / fb
		if per > liveMaxFramesPerPush {
			per = liveMaxFramesPerPush
		}
		sub.frames.window(now, s.frames(), per, func(f, n int) {
			push(&proto.SpectatorInputPush{StartFrame: pb.Int32(int32(f)), FrameBytes: pb.Int32(int32(fb)), InputData: s.inputs[f*fb : (f+n)*fb]})
		})
	}
	if !s.closed.IsZero() && !sub.closeAck && sub.frames.acked == s.frames() && now.Sub(sub.closeSent) >= liveResend {
		sub.closeSent = now
		push(&proto.SpectatorInputPush{StartFrame: pb.Int32(int32(s.frames())), CloseReason: pb.String(s.closeReason)})
	}
}

// window sends chunks of up to per units from next while next < have and within
// liveWindow chunks of acked; back to acked after liveResend without progress.
func (st *liveStream) window(now time.Time, have, per int, send func(off, n int)) {
	if st.next > st.acked && now.Sub(st.progress) >= liveResend {
		st.next = st.acked
		st.progress = now
	}
	for st.next < have && st.next < st.acked+liveWindow*per {
		n := have - st.next
		if n > per {
			n = per
		}
		if st.next == st.acked {
			st.progress = now
		}
		send(st.next, n)
		st.next += n
	}
}

// Sweep drops sessions closed longer than liveRetention or without a push for liveIdle.
func (r *SpectatorRegistry) Sweep(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for code, s := range r.sessions {
		if (!s.closed.IsZero() && now.Sub(s.closed) > liveRetention) || (s.closed.IsZero() && now.Sub(s.lastPush) > liveIdle) {
			r.nsubs -= len(s.subs)
			delete(r.sessions, code)
			glog.Infoln("live drop", code)
		}
	}
}

// cookie binds a subscription to one battle, observed address and epoch (gdxsv's
// subscribeCookie); length prefixes keep the encoding unambiguous.
func (r *SpectatorRegistry) cookie(code string, addr *net.UDPAddr, epoch int64) []byte {
	mac := hmac.New(sha256.New, r.cookieKey[:])
	var enc [8]byte
	for _, v := range []string{code, addr.String()} {
		binary.BigEndian.PutUint64(enc[:], uint64(len(v)))
		mac.Write(enc[:])
		mac.Write([]byte(v))
	}
	binary.BigEndian.PutUint64(enc[:], uint64(epoch))
	mac.Write(enc[:])
	return mac.Sum(nil)[:liveCookieBytes]
}

// validCookie also accepts the previous epoch, so a challenge is good for at least one window.
func (r *SpectatorRegistry) validCookie(code string, addr *net.UDPAddr, c []byte, now time.Time) bool {
	if len(c) != liveCookieBytes {
		return false
	}
	epoch := now.Unix() / int64(liveCookieWindow/time.Second)
	return hmac.Equal(c, r.cookie(code, addr, epoch)) || hmac.Equal(c, r.cookie(code, addr, epoch-1))
}

func sameAddr(a, b *net.UDPAddr) bool {
	return a.Port == b.Port && bytes.Equal(a.IP.To16(), b.IP.To16())
}
