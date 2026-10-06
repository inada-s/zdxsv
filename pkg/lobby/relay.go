package lobby

import (
	"crypto/rand"
	"encoding/binary"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/golang/glog"
)

// A relay server for lobby GGPO battles (pcsx2 ZDXSV_GGPO lobby=1), ported from gdxsv's
// `gdxsv relay` (gdxsv/relay.go): it forwards GGPO packets between the players of a battle
// when that is faster than their direct route.
//   - Packets use GGPO's peer-relay header (type 99 with relay_to_endpoint); the relay
//     restores the original type, like a relaying peer does, and sends the packet to the
//     destination peer of the sender's session.
//   - Players join a session with a 28-byte ping carrying the session id (ggpo_session),
//     peer id and the battle's token (relay_0 in the battle info); only bound members of
//     the same session can relay to each other.
const (
	relayPingMagic   = 0x594c4552 // "RELY"
	relayPingSize    = 28
	relayTypePing    = 1
	relayTypePong    = 2
	relayMaxPeers    = 4
	ggpoConstMagic   = 34046
	ggpoRelayMagic   = 26315
	ggpoTypeRelay    = 99
	ggpoHeaderSize   = 12
	ggpoMinType      = 1 // SyncRequest
	ggpoMaxType      = 8 // AppData
	relayIdleTimeout = 2 * time.Minute
	relayMaxLifetime = 3 * time.Hour
)

type relaySession struct {
	id         uint32
	token      uint64
	peers      [relayMaxPeers]relayPeer
	created    time.Time
	lastActive time.Time
	forwarded  uint64
}

type relayPeer struct {
	addrs   [2]netip.AddrPort // IPv4, IPv6; zero value while unbound
	active  netip.AddrPort    // where packets for this player go
	playing bool              // active is where it sends game packets from, not just where it last pinged from
}

func relayFamily(addr netip.AddrPort) int {
	if addr.Addr().Is4() {
		return 0
	}
	return 1
}

func (p *relayPeer) unbind(addr netip.AddrPort) {
	p.addrs[relayFamily(addr)] = netip.AddrPort{}
	if p.active == addr {
		p.active = netip.AddrPort{}
		p.playing = false
	}
}

type relayBinding struct {
	session *relaySession
	peer    uint8
}

// Relay is safe for concurrent use.
type Relay struct {
	mtx      sync.Mutex
	sessions map[uint32]*relaySession
	bindings map[netip.AddrPort]relayBinding
	now      func() time.Time
}

func NewRelay() *Relay {
	return &Relay{
		sessions: map[uint32]*relaySession{},
		bindings: map[netip.AddrPort]relayBinding{},
		now:      time.Now,
	}
}

// LobbyRelay is the lobby's own relay, nil when disabled; RelayPublicAddr / RelayPublicAddr6
// are the addresses offered to players ("host:port", the IPv6 one may be empty).
var (
	LobbyRelay       *Relay
	RelayPublicAddr  string
	RelayPublicAddr6 string
)

// ServeRelay runs the lobby's relay on addr (both IP families when addr has no host).
func ServeRelay(addr, publicAddr, publicAddr6 string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	r := NewRelay()
	RelayPublicAddr, RelayPublicAddr6 = publicAddr, publicAddr6
	LobbyRelay = r
	glog.Infoln("Start relay", addr, "public", publicAddr, publicAddr6)
	go func() {
		for range time.Tick(10 * time.Second) {
			r.RemoveStaleSessions()
		}
	}()
	r.Serve(conn)
	return nil
}

// SessionToken returns the token of session id, registering the session with a random
// token on first use: every player of a battle asks with the same id and gets the same token.
func (r *Relay) SessionToken(id uint32) uint64 {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	if s, ok := r.sessions[id]; ok {
		return s.token
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	token := binary.LittleEndian.Uint64(b[:])
	now := r.now()
	r.sessions[id] = &relaySession{id: id, token: token, created: now, lastActive: now}
	glog.Infoln("relay session registered", id)
	return token
}

// ServeTestRelay runs a relay on addr without a lobby, for rigs (as gdxsv's
// -relay_test_session): session id with token, registered again while it has expired.
func ServeTestRelay(addr string, id uint32, token uint64) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	r := NewRelay()
	register := func() {
		r.mtx.Lock()
		defer r.mtx.Unlock()
		if _, ok := r.sessions[id]; !ok {
			now := r.now()
			r.sessions[id] = &relaySession{id: id, token: token, created: now, lastActive: now}
			glog.Infoln("relay test session registered", id)
		}
	}
	register()
	glog.Infoln("Start test relay", addr)
	go func() {
		for range time.Tick(10 * time.Second) {
			r.mtx.Lock()
			if s, ok := r.sessions[id]; ok {
				glog.Infoln("relay test session", id, "forwarded", s.forwarded)
			}
			r.mtx.Unlock()
			r.RemoveStaleSessions()
			register()
		}
	}()
	r.Serve(conn)
	return nil
}

func (r *Relay) removeSessionLocked(s *relaySession) {
	for _, p := range s.peers {
		for _, addr := range p.addrs {
			if addr.IsValid() {
				delete(r.bindings, addr)
			}
		}
	}
	delete(r.sessions, s.id)
	glog.Infoln("relay session removed", s.id, "forwarded", s.forwarded)
}

func (r *Relay) RemoveStaleSessions() {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	now := r.now()
	for _, s := range r.sessions {
		if relayIdleTimeout < now.Sub(s.lastActive) || relayMaxLifetime < now.Sub(s.created) {
			r.removeSessionLocked(s)
		}
	}
}

func (r *Relay) Serve(conn *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			glog.Errorln("relay read failed", err)
			return
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		if dst, out := r.handle(buf[:n], from); dst.IsValid() {
			_, _ = conn.WriteToUDPAddrPort(out, dst)
		}
	}
}

// handle returns where to send the (rewritten in place) packet p from from, or an invalid
// address to drop it.
func (r *Relay) handle(p []byte, from netip.AddrPort) (netip.AddrPort, []byte) {
	if len(p) == relayPingSize && binary.LittleEndian.Uint32(p[0:]) == relayPingMagic {
		return r.handlePing(p, from)
	}
	if ggpoHeaderSize <= len(p) &&
		binary.LittleEndian.Uint16(p[0:]) == ggpoConstMagic &&
		p[7] == ggpoTypeRelay &&
		binary.LittleEndian.Uint16(p[8:]) == ggpoRelayMagic {
		return r.handleGgpo(p, from)
	}
	return netip.AddrPort{}, nil
}

// Ping: magic u32, type u8, peer u8, relay index u8, pad u8, session u32, token u64,
// timestamp u64. Binds the sender to its peer slot and is echoed as a Pong.
func (r *Relay) handlePing(p []byte, from netip.AddrPort) (netip.AddrPort, []byte) {
	if p[4] != relayTypePing {
		return netip.AddrPort{}, nil
	}
	peer := p[5]
	sessionID := binary.LittleEndian.Uint32(p[8:])
	token := binary.LittleEndian.Uint64(p[12:])
	if relayMaxPeers <= peer {
		return netip.AddrPort{}, nil
	}

	r.mtx.Lock()
	defer r.mtx.Unlock()
	s, ok := r.sessions[sessionID]
	if !ok || s.token != token {
		return netip.AddrPort{}, nil
	}

	sp := &s.peers[peer]
	if old := sp.addrs[relayFamily(from)]; old != from {
		if old.IsValid() {
			delete(r.bindings, old)
			sp.unbind(old)
		}
		if b, ok := r.bindings[from]; ok {
			b.session.peers[b.peer].unbind(from)
		}
		sp.addrs[relayFamily(from)] = from
		r.bindings[from] = relayBinding{session: s, peer: peer}
		glog.Infoln("relay peer bound", sessionID, peer, from)
	}
	// a ping only moves the player while it is not sending game packets yet: a ping over
	// the other family must not steal the path its game packets use
	if !sp.playing {
		sp.active = from
	}
	s.lastActive = r.now()

	p[4] = relayTypePong
	return from, p
}

// GGPO relay header: magic u16, sequence_number u16, ack u16 (unused), hdr.type u8 = 99,
// relay magic u16, relay_to_endpoint u8 (destination peer), original type u8.
func (r *Relay) handleGgpo(p []byte, from netip.AddrPort) (netip.AddrPort, []byte) {
	to := p[10]
	orgType := p[11]
	if relayMaxPeers <= to || orgType < ggpoMinType || ggpoMaxType < orgType {
		return netip.AddrPort{}, nil
	}

	r.mtx.Lock()
	defer r.mtx.Unlock()
	b, ok := r.bindings[from]
	if !ok || to == b.peer {
		return netip.AddrPort{}, nil
	}
	src := &b.session.peers[b.peer]
	src.active, src.playing = from, true
	dst := b.session.peers[to].active
	if !dst.IsValid() {
		return netip.AddrPort{}, nil
	}
	b.session.lastActive = r.now()
	b.session.forwarded++

	p[7] = orgType
	p[8], p[9], p[10], p[11] = 0, 0, 0, 0
	return dst, p
}
