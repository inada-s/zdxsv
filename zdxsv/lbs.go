package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

type LbsHandler func(*LbsPeer, *LbsMessage)

type handlerHolder struct {
	handlers     map[uint16]LbsHandler
	handlerNames map[uint16]string
}

var defaultHandlers = &handlerHolder{
	handlers:     make(map[uint16]LbsHandler),
	handlerNames: make(map[uint16]string),
}

func register(id uint16, name string, f LbsHandler) interface{} {
	defaultHandlers.handlers[id] = f
	defaultHandlers.handlerNames[id] = name
	return nil
}

type eventPeerCome struct {
	peer *LbsPeer
}

type eventPeerLeave struct {
	peer *LbsPeer
}

type eventPeerMessage struct {
	peer *LbsPeer
	msg  *LbsMessage
}

type eventFunc struct {
	f func(*Lbs)
	c chan<- interface{}
}

type LbsPeer struct {
	LbsUser
	conn              *LbsConn
	app               *Lbs
	inBattleAfterRoom bool
	Platform          string
	PlatformInfo      map[string]string

	proxyIP           net.IP
	proxyPort         uint16
	proxyUDPAddrs     []string
	proxyP2PConnected map[string]struct{}

	proxyRegTime time.Time
	proxyUseTime time.Time

	openTime     time.Time
	lastRecvTime time.Time
}

func (p *LbsPeer) OnOpen() {
	p.app.chEvent <- eventPeerCome{peer: p}
}

func (p *LbsPeer) OnMessage(msg *LbsMessage) {
	p.app.chEvent <- eventPeerMessage{peer: p, msg: msg}
}

func (p *LbsPeer) OnClose() {
	p.app.chEvent <- eventPeerLeave{peer: p}
}

func (p *LbsPeer) SendMessage(msg *LbsMessage) {
	p.conn.SendMessage(msg)
}

func (p *LbsPeer) IP() string {
	h, _, err := net.SplitHostPort(p.conn.Address())
	if err != nil {
		return ""
	}
	return h
}

type Lbs struct {
	*handlerHolder
	mcs     *Mcs
	users   map[string]*LbsPeer
	lobbys  map[string]map[uint16]*LbsLobby // platform -> lobby id -> lobby
	battles map[string]*LbsBattle
	peers   map[string]*LbsPeer // conn address -> peer, owned by Serve
	chEvent chan interface{}
	chQuit  chan interface{}

	aliveCheckInterval time.Duration
	relayProv          *relayProvisioning
	relayPeers         map[*LbsPeer]*RegionalRelay
	carryCode          func(loginKey string) string
	proxyLimit         *ipLimiter
	battleInfoLimit    *ipLimiter
}

const (
	peerRecvTimeout  = 2 * time.Minute
	peerLoginTimeout = 5 * time.Minute
	battleTimeout    = time.Hour
)

func NewLbs() *Lbs {
	app := &Lbs{
		handlerHolder:      defaultHandlers,
		users:              make(map[string]*LbsPeer),
		lobbys:             make(map[string]map[uint16]*LbsLobby),
		battles:            make(map[string]*LbsBattle),
		peers:              make(map[string]*LbsPeer),
		relayPeers:         make(map[*LbsPeer]*RegionalRelay),
		chEvent:            make(chan interface{}, 64),
		chQuit:             make(chan interface{}),
		aliveCheckInterval: 10 * time.Second,
		proxyLimit:         &ipLimiter{window: 10 * time.Second, perIP: proxyRegistersPerIP},
		battleInfoLimit:    &ipLimiter{window: 10 * time.Second, perIP: proxyBattleInfosPerIP},
	}
	app.getLobby(PlatformConsole, 0)
	app.getLobby(PlatformEmu, 0)
	return app
}

// getLobby returns the lobby of the platform, creating the platform's lobbies on first use.
func (a *Lbs) getLobby(platform string, lobbyID uint16) (*LbsLobby, bool) {
	lobbys, ok := a.lobbys[platform]
	if !ok {
		lobbys = make(map[uint16]*LbsLobby)
		for i := 0; i < LobbyCount; i++ {
			lobbys[uint16(i)] = NewLobby(platform, uint16(i))
		}
		a.lobbys[platform] = lobbys
	}
	l, ok := lobbys[lobbyID]
	return l, ok
}

func (a *Lbs) NewPeer(conn *LbsConn) LbsConnPeer {
	return &LbsPeer{
		conn:     conn,
		app:      a,
		Platform: PlatformConsole,
	}
}

func (a *Lbs) Locked(f func(*Lbs)) {
	c := make(chan interface{})
	a.chEvent <- eventFunc{
		f: f,
		c: c,
	}
	<-c
}

func (a *Lbs) Quit() {
	a.Locked(func(app *Lbs) {
		for _, p := range app.users {
			SendServerShutDown(p)
		}
	})
	time.Sleep(10 * time.Millisecond)
	close(a.chQuit)
}

func (a *Lbs) AddHandler(id uint16, name string, f LbsHandler) {
	a.handlers[id] = f
	a.handlerNames[id] = name
}

// SetBattleServer gives the lobby the battle server (Mcs) it hands battle users to. Call it before Serve.
func (a *Lbs) SetBattleServer(b *Mcs) {
	a.mcs = b
}

// preLoginCommands are what a peer may send before its user logs in: the login sequence,
// the emulator and relay reports, and the line keepers.
var preLoginCommands = map[uint16]bool{
	0x0001:         true,
	0x6001:         true,
	0x6006:         true,
	0x600E:         true,
	0x6101:         true,
	0x6103:         true,
	0x6132:         true,
	0x61A0:         true,
	0x9950:         true,
	0x9952:         true,
	lbsRelayStatus: true,
}

var loginCommands = map[uint16]bool{
	0x6101: true,
	0x6103: true,
	0x6132: true,
}

func (a *Lbs) loggedIn(p *LbsPeer) bool {
	return p.UserID != "" && a.users[p.UserID] == p
}

// accepts tells whether p may send the command now.
func (a *Lbs) accepts(p *LbsPeer, command uint16) bool {
	switch {
	case a.loggedIn(p):
		return true
	case command == 0x6132:
		return p.LoginKey != ""
	default:
		return preLoginCommands[command]
	}
}

func (a *Lbs) Serve() {
	aliveCheck := time.NewTicker(a.aliveCheckInterval)
	defer aliveCheck.Stop()

	for {
		select {
		case <-a.chQuit:
			return
		case e := <-a.chEvent:
			switch args := e.(type) {
			case eventPeerCome:
				floodLog.Info("eventPeerCome")
				args.peer.openTime = time.Now()
				args.peer.lastRecvTime = args.peer.openTime
				a.peers[args.peer.conn.Address()] = args.peer
				a.OnOpen(args.peer)
			case eventPeerMessage:
				if a.peers[args.peer.conn.Address()] != args.peer {
					continue
				}
				args.peer.lastRecvTime = time.Now()
				if a.loggedIn(args.peer) && loginCommands[args.msg.Command] {
					floodLog.Warn("login command from a logged-in peer, closing it", zap.Any("name", symbolMap[args.msg.Command]), zap.String("user_id", args.peer.UserID))
					a.closePeer(args.peer)
					continue
				}
				if !a.accepts(args.peer, args.msg.Command) {
					floodLog.Warn("command before login ignored", zap.Any("name", symbolMap[args.msg.Command]), zap.Uint16("command", args.msg.Command), zap.Any("addr", args.peer.conn.Address()))
					continue
				}
				if f, ok := a.handlers[args.msg.Command]; ok {
					f(args.peer, args.msg)
				} else {
					floodLog.Error("handler not found", zap.Any("name", symbolMap[args.msg.Command]), zap.Any("msg", args.msg))
					if args.msg.Category == CategoryQuestion {
						args.peer.SendMessage(NewServerAnswer(args.msg))
					}
				}
			case eventPeerLeave:
				floodLog.Info("eventPeerLeave")
				a.OnClose(args.peer)
				delete(a.peers, args.peer.conn.Address())
			case eventFunc:
				args.f(a)
				args.c <- struct{}{}
			}
		case now := <-aliveCheck.C:
			a.checkAlive(now)
			a.updateRelays(now)
		}
	}
}

// checkAlive closes peers silent for peerRecvTimeout and line-checks the
// others, drops battles older than battleTimeout and keeps the battle
// server connection up.
func (a *Lbs) checkAlive(now time.Time) {
	for _, p := range a.peers {
		if now.Sub(p.lastRecvTime) >= peerRecvTimeout {
			logger.Info("recv timeout", zap.String("user_id", p.UserID), zap.Any("addr", p.conn.Address()))
			p.conn.conn.Close()
		} else if now.Sub(p.openTime) >= peerLoginTimeout && a.users[p.UserID] != p && a.relayPeers[p] == nil {
			logger.Info("login timeout", zap.String("user_id", p.UserID), zap.Any("addr", p.conn.Address()))
			p.conn.conn.Close()
		} else {
			RequestLineCheck(p)
		}
	}

	for sid, battle := range a.battles {
		if now.Sub(battle.StartTime) >= battleTimeout {
			delete(a.battles, sid)
			logger.Info("battle user timeout", zap.Any("session_id", sid), zap.Any("battle", battle))
		}
	}

}

const (
	EntryNone   = 0
	EntryAeug   = 1
	EntryTitans = 2
)

type LbsUser struct {
	DBUser
	Entry byte
	Bin   string

	Room   *LbsRoom
	Lobby  *LbsLobby
	Battle *LbsBattle
}

// DecodeSessionID turns the game's value (up to 10 digits) into the 8 digit session id:
// value - 100001, zero padded to 10 digits, without digits 0 and 5.
func DecodeSessionID(value string) (string, error) {
	if len(value) == 0 || len(value) > 10 {
		return "", fmt.Errorf("session id value %q: want 1 to 10 digits", value)
	}
	v, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return "", fmt.Errorf("session id value %q: %w", value, err)
	}
	if v < 100001 {
		return "", fmt.Errorf("session id value %q: below 100001", value)
	}
	s := fmt.Sprintf("%010d", v-100001)
	return s[1:5] + s[6:], nil
}

// Platform keeps emulator and real PS2 players apart:
// lobbies, rooms and battles are per platform.
// Every peer is a console until it sends a platform info message.
const (
	PlatformConsole = "console"
	// every emulator, whatever its cpu: ARM64 and x86_64 builds stay in sync.
	// The value is the status API key.
	PlatformEmu = "emu"
)

// ParsePlatformInfo parses "key=value" lines.
func ParsePlatformInfo(body string) map[string]string {
	info := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		kv := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(kv) == 2 && kv[0] != "" {
			info[kv[0]] = kv[1]
		}
	}
	return info
}

func PlatformFromInfo(info map[string]string) string {
	if info["emulator"] == "" {
		return PlatformConsole
	}
	return PlatformEmu
}
