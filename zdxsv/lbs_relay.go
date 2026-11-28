package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

// A relay server for lobby GGPO battles: it forwards GGPO packets between the players of a battle
// when that is faster than their direct route.
//   - Packets use GGPO's peer-relay header (type 99 with relay_to_endpoint); the relay
//     restores the original type, like a relaying peer does, and sends the packet to the
//     destination peer of the sender's session.
//   - Players join a session with a 28-byte ping carrying the session id (ggpo_session),
//     peer id and the battle's token (relay_0 in the battle info); only bound members of
//     the same session can relay to each other.
//   - Before the battle, a player that reaches no other directly still sends its RTTs (ping
//     test packets of type pingTestTypeRtts) to the others through the relay, unchanged; the
//     relay checks the header only.
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
	pingTestMagic    = 2205246188
	pingTestTypeRtts = 3
	pingTestMinSize  = 44
	pingTestMaxSize  = 256
	relayIdleTimeout = 2 * time.Minute
	relayMaxLifetime = 3 * time.Hour

	relayMaintainInterval = 10 * time.Second
)

type relaySession struct {
	id         uint32
	token      uint64
	peers      [relayMaxPeers]relayPeer
	created    time.Time
	lastActive time.Time
	forwarded  uint64 // GGPO packets
	rtts       uint64 // ping test packets
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

// ServeRelay runs the lobby's relay on conn until it is closed.
func ServeRelay(conn *net.UDPConn, publicAddr, publicAddr6 string) error {
	r := NewRelay()
	RelayPublicAddr, RelayPublicAddr6 = publicAddr, publicAddr6
	LobbyRelay = r
	logger.Info("start relay", zap.Any("public", publicAddr), zap.Any("public6", publicAddr6))
	r.serveMaintained(conn, relayMaintainInterval, r.RemoveStaleSessions)
	return errors.New("relay stopped")
}

func (r *Relay) serveMaintained(conn *net.UDPConn, interval time.Duration, maintain func()) {
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-ticker.C:
				maintain()
			case <-done:
				return
			}
		}
	}()
	r.Serve(conn)
	ticker.Stop()
	close(done)
	<-stopped
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
	logger.Info("relay session registered", zap.Any("session_id", id))
	return token
}

// ServeTestRelay runs a relay on addr without a lobby, for tests: session id with token,
// registered again while it has expired.
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
	r, maintain := newTestRelay(id, token)
	logger.Info("start test relay", zap.Any("addr", addr))
	r.serveMaintained(conn, relayMaintainInterval, maintain)
	return nil
}

// newTestRelay returns a relay with session id registered with token, and its
// maintenance: drop stale sessions, register id again if it was dropped.
func newTestRelay(id uint32, token uint64) (*Relay, func()) {
	r := NewRelay()
	register := func() {
		r.mtx.Lock()
		defer r.mtx.Unlock()
		if _, ok := r.sessions[id]; !ok {
			now := r.now()
			r.sessions[id] = &relaySession{id: id, token: token, created: now, lastActive: now}
			logger.Info("relay test session registered", zap.Any("session_id", id))
		}
	}
	register()
	return r, func() {
		r.mtx.Lock()
		if s, ok := r.sessions[id]; ok {
			logger.Info("relay test session", zap.Any("session_id", id), zap.Any("forwarded", s.forwarded), zap.Any("rtts", s.rtts))
		}
		r.mtx.Unlock()
		r.RemoveStaleSessions()
		register()
	}
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
	logger.Info("relay session removed", zap.Any("session_id", s.id), zap.Any("forwarded", s.forwarded), zap.Any("rtts", s.rtts))
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
			logger.Error("relay read failed", zap.Error(err))
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
	if pingTestMinSize <= len(p) && len(p) <= pingTestMaxSize && binary.LittleEndian.Uint32(p[0:]) == pingTestMagic {
		return r.handlePingTest(p, from)
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
		floodLog.Info("relay peer bound", zap.Any("session_id", sessionID), zap.Any("peer", peer), zap.Stringer("addr", from))
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

// Ping test packet: magic u32, session u32, type u8, from peer u8, to peer u8, then what only the
// players read. Sent unchanged to the to peer of the sender's session. Only pingTestTypeRtts: a
// ping or pong through the relay would make an older client take the relay for the peer.
func (r *Relay) handlePingTest(p []byte, from netip.AddrPort) (netip.AddrPort, []byte) {
	sessionID := binary.LittleEndian.Uint32(p[4:])
	typ, fromPeer, to := p[8], p[9], p[10]
	if typ != pingTestTypeRtts || relayMaxPeers <= to {
		return netip.AddrPort{}, nil
	}

	r.mtx.Lock()
	defer r.mtx.Unlock()
	b, ok := r.bindings[from]
	if !ok || b.session.id != sessionID || fromPeer != b.peer || to == b.peer {
		return netip.AddrPort{}, nil
	}
	dst := b.session.peers[to].active
	if !dst.IsValid() {
		return netip.AddrPort{}, nil
	}
	b.session.lastActive = r.now()
	b.session.rtts++
	return dst, p
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

// maxRelaysPerBattle is the number of relay_<k> lines a client accepts.
const maxRelaysPerBattle = 4

// relayUnreachableMs is the relayed RTT of a player pair when either has no latency for the relay's region.
const relayUnreachableMs = 999

// RelaySessions issues the token of a battle's relay session; the same id gives the same token.
type RelaySessions interface {
	SessionToken(id uint32) uint64
}

// RegionalRelay is a relay in a cloud region, registered with the lobby. Region is a
// "<region>=<ms>" platform info key.
type RegionalRelay struct {
	Region      string
	PublicAddr  string
	PublicAddr6 string
	Sessions    RelaySessions

	// set by the relay control connection, under regionalRelays
	activeSessions int
	shutdown       func()
}

var regionalRelays = struct {
	sync.Mutex
	byAddr map[string]*RegionalRelay
}{byAddr: map[string]*RegionalRelay{}}

// RegisterRelay offers r to battles until UnregisterRelay(r.PublicAddr); a relay with the same
// PublicAddr is replaced.
func RegisterRelay(r *RegionalRelay) {
	regionalRelays.Lock()
	defer regionalRelays.Unlock()
	regionalRelays.byAddr[r.PublicAddr] = r
}

func UnregisterRelay(publicAddr string) {
	regionalRelays.Lock()
	defer regionalRelays.Unlock()
	delete(regionalRelays.byAddr, publicAddr)
}

// battleRelays lists the relays offered to a battle of users, best first: regional relays by
// the worst relayed RTT of a player pair (the sum of both players' latencies to the relay's
// region), then the lobby's own relay, at most maxRelaysPerBattle. Clients settle on the
// earlier relay when they disagree, so the order depends only on the battle, never on the
// player asking.
func battleRelays(users []string, info func(userID string) map[string]string) []*RegionalRelay {
	latency := func(id, region string) int {
		ms, err := strconv.Atoi(info(id)[region])
		if err != nil || ms <= 0 {
			return 0
		}
		return ms
	}
	type scored struct {
		r     *RegionalRelay
		worst int
	}
	var list []scored
	regionalRelays.Lock()
	for _, r := range regionalRelays.byAddr {
		worst := 0
		for i := range users {
			for j := i + 1; j < len(users); j++ {
				a, b := latency(users[i], r.Region), latency(users[j], r.Region)
				via := relayUnreachableMs
				if a > 0 && b > 0 {
					via = a + b
				}
				worst = max(worst, via)
			}
		}
		list = append(list, scored{r, worst})
	}
	regionalRelays.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].worst != list[j].worst {
			return list[i].worst < list[j].worst
		}
		return list[i].r.PublicAddr < list[j].r.PublicAddr
	})
	var relays []*RegionalRelay
	for _, s := range list {
		relays = append(relays, s.r)
	}
	if LobbyRelay != nil && RelayPublicAddr != "" {
		relays = append(relays, &RegionalRelay{PublicAddr: RelayPublicAddr, PublicAddr6: RelayPublicAddr6, Sessions: LobbyRelay})
	}
	if len(relays) > maxRelaysPerBattle {
		relays = relays[:maxRelaysPerBattle]
	}
	return relays
}

// A regional relay (`zdxsv relay`) keeps a TCP connection to the lobby's public address: it
// sends a RelayStatus notice (lbsRelayStatus) every relayStatusInterval, the lobby pushes it a
// RelayControl notice (lbsRelayControl) with each battle session's token as the battle is
// offered the relay. The relay is offered while the connection lives.
const (
	lbsRelayStatus  = 0x9901
	lbsRelayControl = 0x9902

	relayStatusInterval   = 10 * time.Second
	relayReconnectDelay   = 10 * time.Second
	relayLobbyLostTimeout = 15 * time.Minute
)

// RelayStatus is a relay's notice to the lobby. Secret is ZDXSV_RELAY_SECRET: anyone can
// reach the lobby, and the lobby hands its relays to players.
type RelayStatus struct {
	Region      string `json:"region,omitempty"`
	PublicAddr  string `json:"public_addr,omitempty"`
	PublicAddr6 string `json:"public_addr6,omitempty"`
	Sessions    int    `json:"sessions"`
	Secret      string `json:"secret,omitempty"`
}

type RelayControl struct {
	Sessions []RelayControlSession `json:"sessions,omitempty"`
	Shutdown bool                  `json:"shutdown,omitempty"`
}

type RelayControlSession struct {
	SessionID uint32 `json:"session_id"`
	Token     uint64 `json:"token"`
}

// remoteRelaySessions makes the tokens of a connected relay's sessions and sends them to it.
type remoteRelaySessions struct {
	mtx    sync.Mutex
	tokens map[uint32]remoteRelayToken
	send   func(RelayControl)
	now    func() time.Time
}

type remoteRelayToken struct {
	token   uint64
	created time.Time
}

func newRemoteRelaySessions(send func(RelayControl)) *remoteRelaySessions {
	return &remoteRelaySessions{
		tokens: map[uint32]remoteRelayToken{},
		send:   send,
		now:    time.Now,
	}
}

func (s *remoteRelaySessions) SessionToken(id uint32) uint64 {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	if t, ok := s.tokens[id]; ok {
		return t.token
	}
	now := s.now()
	for k, t := range s.tokens {
		if relayMaxLifetime < now.Sub(t.created) {
			delete(s.tokens, k)
		}
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	token := binary.LittleEndian.Uint64(b[:])
	s.tokens[id] = remoteRelayToken{token, now}
	s.send(RelayControl{Sessions: []RelayControlSession{{SessionID: id, Token: token}}})
	return token
}

func sendRelayControl(p *LbsPeer, ctl RelayControl) {
	body, err := json.Marshal(ctl)
	if err != nil {
		logger.Error("relay control", zap.Error(err))
		return
	}
	n := NewServerNotice(lbsRelayControl)
	n.Writer().Write(body)
	p.SendMessage(n)
}

func (a *Lbs) OnRelayStatus(p *LbsPeer, body []byte) {
	var st RelayStatus
	if err := json.Unmarshal(body, &st); err != nil {
		floodLog.Warn("invalid relay status", zap.Any("addr", p.conn.Address()), zap.Error(err))
		return
	}
	secret := string(Conf.Lobby.RelaySecret)
	if secret == "" || subtle.ConstantTimeCompare([]byte(st.Secret), []byte(secret)) != 1 {
		floodLog.Warn("relay status with a wrong secret", zap.Any("addr", p.conn.Address()), zap.String("public", st.PublicAddr))
		sendRelayControl(p, RelayControl{Shutdown: true})
		return
	}
	r := a.relayPeers[p]
	if r == nil {
		if st.PublicAddr == "" {
			return
		}
		r = &RegionalRelay{Region: st.Region, PublicAddr: st.PublicAddr, PublicAddr6: st.PublicAddr6,
			Sessions: newRemoteRelaySessions(func(ctl RelayControl) { sendRelayControl(p, ctl) })}
		r.shutdown = func() { sendRelayControl(p, RelayControl{Shutdown: true}) }
		a.relayPeers[p] = r
		RegisterRelay(r)
		logger.Info("relay registered", zap.String("region", st.Region), zap.String("public", st.PublicAddr), zap.String("public6", st.PublicAddr6), zap.Any("addr", p.conn.Address()))
	}
	regionalRelays.Lock()
	r.activeSessions = st.Sessions
	regionalRelays.Unlock()
}

func (a *Lbs) closeRelayPeer(p *LbsPeer) {
	r := a.relayPeers[p]
	if r == nil {
		return
	}
	delete(a.relayPeers, p)
	unregisterRelayIf(r)
	logger.Info("relay unregistered", zap.String("region", r.Region), zap.String("public", r.PublicAddr))
}

// unregisterRelayIf removes r unless another relay with its address replaced it.
func unregisterRelayIf(r *RegionalRelay) {
	regionalRelays.Lock()
	defer regionalRelays.Unlock()
	if regionalRelays.byAddr[r.PublicAddr] == r {
		delete(regionalRelays.byAddr, r.PublicAddr)
	}
}

func (r *Relay) SetSessionToken(id uint32, token uint64) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	if s, ok := r.sessions[id]; ok {
		if s.token == token {
			return
		}
		r.removeSessionLocked(s)
	}
	now := r.now()
	r.sessions[id] = &relaySession{id: id, token: token, created: now, lastActive: now}
	logger.Info("relay session registered", zap.Any("session_id", id))
}

func (r *Relay) sessionCount() int {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	return len(r.sessions)
}

// RunRegionalRelay relays on conn and keeps it registered with the lobby at lobbyAddr, reconnecting
// until ctx ends, the lobby asks it to shut down, or it has had no lobby for relayLobbyLostTimeout.
func RunRegionalRelay(ctx context.Context, conn *net.UDPConn, lobbyAddr string, status RelayStatus) error {
	r := NewRelay()
	go r.serveMaintained(conn, relayMaintainInterval, r.RemoveStaleSessions)
	logger.Info("start regional relay", zap.Stringer("addr", conn.LocalAddr()), zap.String("region", status.Region), zap.String("public", status.PublicAddr), zap.String("public6", status.PublicAddr6), zap.Any("lobby", lobbyAddr))
	lastConnected := time.Now()
	for {
		shutdown, err := r.controlSession(ctx, lobbyAddr, status, &lastConnected)
		if shutdown {
			logger.Info("relay shutdown by the lobby")
			return nil
		}
		logger.Warn("relay lost the lobby", zap.Error(err))
		if relayLobbyLostTimeout < time.Since(lastConnected) {
			return errors.New("no lobby for a while")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(relayReconnectDelay):
		}
	}
}

func (r *Relay) controlSession(ctx context.Context, lobbyAddr string, status RelayStatus, lastConnected *time.Time) (bool, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", lobbyAddr)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	logger.Info("relay connected to the lobby", zap.String("lobby", lobbyAddr))

	*lastConnected = time.Now()
	defer func() { *lastConnected = time.Now() }()
	sendErr := make(chan error, 1)
	go func() {
		for {
			status.Sessions = r.sessionCount()
			body, _ := json.Marshal(status)
			m := NewClientNotice(lbsRelayStatus)
			m.Writer().Write(body)
			conn.SetWriteDeadline(time.Now().Add(relayStatusInterval))
			if _, err := conn.Write(m.Serialize()); err != nil {
				sendErr <- err
				conn.Close()
				return
			}
			select {
			case <-time.After(relayStatusInterval):
			case <-ctx.Done():
				return
			}
		}
	}()

	buf := make([]byte, 4096)
	var data []byte
	for {
		n, err := conn.Read(buf)
		if err != nil {
			select {
			case e := <-sendErr:
				return false, e
			default:
				return false, err
			}
		}
		data = append(data, buf[:n]...)
		for len(data) >= HeaderSize {
			n, msg := Deserialize(data)
			if n == 0 {
				break
			}
			data = data[n:]
			if msg.Command != lbsRelayControl {
				continue
			}
			var ctl RelayControl
			if err := json.Unmarshal(msg.Body, &ctl); err != nil {
				logger.Error("invalid relay control", zap.Error(err))
				continue
			}
			for _, s := range ctl.Sessions {
				r.SetSessionToken(s.SessionID, s.Token)
			}
			if ctl.Shutdown {
				return true, nil
			}
		}
	}
}

const (
	relayMinUsers     = 4
	relayIdleShutdown = 30 * time.Minute
	// relayStartRetry is how long a started region may take to register before it is started again.
	relayStartRetry = 10 * time.Minute
)

// relayRegionGroup is the area of each "<region>=<ms>" key players report; players whose best
// regions are in different groups may play across a long route.
var relayRegionGroup = map[string]string{
	"asia-east1":              "asia-east",
	"asia-east2":              "asia-east",
	"asia-northeast1":         "asia-northeast",
	"asia-northeast2":         "asia-northeast",
	"asia-northeast3":         "asia-northeast",
	"asia-southeast1":         "asia-southeast",
	"australia-southeast1":    "australia-southeast",
	"europe-west2":            "europe-west",
	"northamerica-northeast1": "northamerica-northeast",
	"southamerica-east1":      "southamerica-east",
	"us-central1":             "us-central",
	"us-east4":                "us-east",
	"us-west2":                "us-west",
}

// RelayProvisioner starts a regional relay (a VM, a container) in a region; the relay then
// registers through the relay control address. StartRelay must not block the lobby.
type RelayProvisioner interface {
	StartRelay(region string)
}

type relayProvisioning struct {
	regions    []string
	prov       RelayProvisioner
	lastNeeded map[string]time.Time
	lastStart  map[string]time.Time
}

// SetRelayProvisioning makes the lobby start a relay in each of regions while relays may be
// needed, and shut down a relay of these regions once unneeded for relayIdleShutdown with no
// sessions. Call before Serve.
func (a *Lbs) SetRelayProvisioning(regions []string, prov RelayProvisioner) {
	a.relayProv = &relayProvisioning{
		regions:    regions,
		prov:       prov,
		lastNeeded: map[string]time.Time{},
		lastStart:  map[string]time.Time{},
	}
}

// relayNeeded reports whether a battle's worth of GGPO players is online from at
// least two region groups.
func (a *Lbs) relayNeeded() bool {
	users := 0
	first, mixed := "", false
	for _, p := range a.users {
		if ggpoPort(p.PlatformInfo) == 0 {
			continue
		}
		users++
		g := relayRegionGroup[bestRelayRegion(p.PlatformInfo)]
		if g == "" {
			continue
		}
		if first == "" {
			first = g
		} else if g != first {
			mixed = true
		}
	}
	return users >= relayMinUsers && mixed
}

func bestRelayRegion(info map[string]string) string {
	best, bestMs := "", 0
	for region := range relayRegionGroup {
		ms, err := strconv.Atoi(info[region])
		if err != nil || ms <= 0 {
			continue
		}
		if best == "" || ms < bestMs || ms == bestMs && region < best {
			best, bestMs = region, ms
		}
	}
	return best
}

func (a *Lbs) updateRelays(now time.Time) {
	rp := a.relayProv
	if rp == nil {
		return
	}
	regionalRelays.Lock()
	running := map[string]bool{}
	for _, r := range regionalRelays.byAddr {
		running[r.Region] = true
	}
	regionalRelays.Unlock()

	needed := a.relayNeeded()
	for _, region := range rp.regions {
		if needed {
			rp.lastNeeded[region] = now
		} else if _, ok := rp.lastNeeded[region]; !ok && running[region] {
			// a relay already running when the lobby started gets the full grace period
			rp.lastNeeded[region] = now
		}
		if needed && !running[region] && now.Sub(rp.lastStart[region]) >= relayStartRetry {
			rp.lastStart[region] = now
			logger.Info("relay needed, starting one", zap.Any("region", region))
			rp.prov.StartRelay(region)
		}
	}

	regionalRelays.Lock()
	defer regionalRelays.Unlock()
	for _, r := range regionalRelays.byAddr {
		last, ok := rp.lastNeeded[r.Region]
		if !ok {
			rp.lastNeeded[r.Region] = now
			continue
		}
		if now.Sub(last) < relayIdleShutdown || r.activeSessions != 0 || r.shutdown == nil {
			continue
		}
		logger.Info("relay no longer needed, shutting it down", zap.String("region", r.Region), zap.String("public", r.PublicAddr))
		r.shutdown()
		r.shutdown = nil
	}
}
