package lobby

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"net"
	"sync"
	"time"

	"github.com/golang/glog"
)

// A regional relay (`zdxsv relay`) keeps a TCP connection to the lobby's relay control
// address: it sends a RelayStatus every relayStatusInterval, the lobby pushes it a
// RelayControl with each battle session's token as the battle is offered the relay. The
// relay is offered while the connection lives.
const (
	relayStatusInterval  = 10 * time.Second
	relayStatusTimeout   = 3 * relayStatusInterval
	relayReconnectDelay  = 5 * time.Second
	relayControlQueueLen = 256
)

// RelayStatus is a relay's line to the lobby. Secret is ZDXSV_RELAY_SECRET: anyone can
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

// remoteRelaySessions makes the tokens of a connected relay's sessions and queues them to it.
type remoteRelaySessions struct {
	mtx    sync.Mutex
	tokens map[uint32]remoteRelayToken
	out    chan RelayControl
	now    func() time.Time
}

type remoteRelayToken struct {
	token   uint64
	created time.Time
}

func newRemoteRelaySessions() *remoteRelaySessions {
	return &remoteRelaySessions{
		tokens: map[uint32]remoteRelayToken{},
		out:    make(chan RelayControl, relayControlQueueLen),
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
	select {
	case s.out <- RelayControl{Sessions: []RelayControlSession{{SessionID: id, Token: token}}}:
	default:
		glog.Errorln("relay control queue full, session dropped", id)
	}
	return token
}

// ServeRelayControl accepts regional relays on l until it is closed; relays must send secret.
func ServeRelayControl(l net.Listener, secret string) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go serveRelayControlConn(conn, secret)
	}
}

func serveRelayControlConn(conn net.Conn, secret string) {
	defer conn.Close()
	sessions := newRemoteRelaySessions()
	var registered *RegionalRelay
	defer func() {
		if registered != nil {
			unregisterRelayIf(registered)
			glog.Infoln("relay unregistered", registered.Region, registered.PublicAddr)
		}
	}()
	done := make(chan struct{})
	defer close(done)
	go func() {
		enc := json.NewEncoder(conn)
		for {
			select {
			case ctl := <-sessions.out:
				conn.SetWriteDeadline(time.Now().Add(relayStatusInterval))
				if err := enc.Encode(ctl); err != nil {
					conn.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()
	dec := json.NewDecoder(conn)
	for {
		conn.SetReadDeadline(time.Now().Add(relayStatusTimeout))
		var st RelayStatus
		if err := dec.Decode(&st); err != nil {
			return
		}
		if secret == "" || subtle.ConstantTimeCompare([]byte(st.Secret), []byte(secret)) != 1 {
			glog.Warningln("relay status with a wrong secret from", conn.RemoteAddr(), st.PublicAddr)
			return
		}
		if registered == nil {
			if st.PublicAddr == "" {
				return
			}
			registered = &RegionalRelay{Region: st.Region, PublicAddr: st.PublicAddr, PublicAddr6: st.PublicAddr6, Sessions: sessions}
			registered.shutdown = func() {
				select {
				case sessions.out <- RelayControl{Shutdown: true}:
				default:
					glog.Errorln("relay control queue full, shutdown dropped", st.PublicAddr)
				}
			}
			RegisterRelay(registered)
			glog.Infoln("relay registered", st.Region, st.PublicAddr, st.PublicAddr6, "from", conn.RemoteAddr())
		}
		regionalRelays.Lock()
		registered.activeSessions = st.Sessions
		regionalRelays.Unlock()
	}
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
	glog.Infoln("relay session registered", id)
}

func (r *Relay) sessionCount() int {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	return len(r.sessions)
}

// RunRegionalRelay relays on conn and keeps it registered with the lobby's relay control
// address, reconnecting until ctx ends or the lobby asks it to shut down.
func RunRegionalRelay(ctx context.Context, conn *net.UDPConn, lobbyAddr string, status RelayStatus) error {
	r := NewRelay()
	go r.serveMaintained(conn, relayMaintainInterval, r.RemoveStaleSessions)
	glog.Infoln("Start regional relay", conn.LocalAddr(), "region", status.Region, "public", status.PublicAddr, status.PublicAddr6, "lobby", lobbyAddr)
	for {
		shutdown, err := r.controlSession(ctx, lobbyAddr, status)
		if shutdown {
			glog.Infoln("relay shutdown by the lobby")
			return nil
		}
		glog.Warningln("relay control:", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(relayReconnectDelay):
		}
	}
}

func (r *Relay) controlSession(ctx context.Context, lobbyAddr string, status RelayStatus) (bool, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", lobbyAddr)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	sendErr := make(chan error, 1)
	go func() {
		enc := json.NewEncoder(conn)
		for {
			status.Sessions = r.sessionCount()
			conn.SetWriteDeadline(time.Now().Add(relayStatusInterval))
			if err := enc.Encode(status); err != nil {
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
	dec := json.NewDecoder(conn)
	for {
		var ctl RelayControl
		if err := dec.Decode(&ctl); err != nil {
			select {
			case e := <-sendErr:
				return false, e
			default:
				return false, err
			}
		}
		for _, s := range ctl.Sessions {
			r.SetSessionToken(s.SessionID, s.Token)
		}
		if ctl.Shutdown {
			return true, nil
		}
	}
}
