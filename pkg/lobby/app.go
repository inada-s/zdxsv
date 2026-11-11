package lobby

import (
	"fmt"
	"net"
	"time"

	"zdxsv/pkg/battle"
	"zdxsv/pkg/lobby/model"
	"zdxsv/pkg/message"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
)

type LbsHandler func(*LbsPeer, *message.LbsMessage)

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
	msg  *message.LbsMessage
}

type eventFunc struct {
	f func(*Lbs)
	c chan<- interface{}
}

type LbsPeer struct {
	model.LbsUser
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

	lastRecvTime time.Time
}

func (p *LbsPeer) OnOpen() {
	p.app.chEvent <- eventPeerCome{peer: p}
}

func (p *LbsPeer) OnMessage(msg *message.LbsMessage) {
	p.app.chEvent <- eventPeerMessage{peer: p, msg: msg}
}

func (p *LbsPeer) OnClose() {
	p.app.chEvent <- eventPeerLeave{peer: p}
}

func (p *LbsPeer) SendMessage(msg *message.LbsMessage) {
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
	battleServer *battle.Mcs
	users        map[string]*LbsPeer
	lobbys       map[string]map[uint16]*model.LbsLobby // platform -> lobby id -> lobby
	battles      map[string]*model.LbsBattle
	peers        map[string]*LbsPeer // conn address -> peer, owned by Serve
	chEvent      chan interface{}
	chQuit       chan interface{}

	aliveCheckInterval time.Duration
	relayProv          *relayProvisioning
	carryCode          func(loginKey string) string
}

const (
	peerRecvTimeout = 2 * time.Minute
	battleTimeout   = time.Hour
)

func NewLbs() *Lbs {
	app := &Lbs{
		handlerHolder:      defaultHandlers,
		users:              make(map[string]*LbsPeer),
		lobbys:             make(map[string]map[uint16]*model.LbsLobby),
		battles:            make(map[string]*model.LbsBattle),
		peers:              make(map[string]*LbsPeer),
		chEvent:            make(chan interface{}, 64),
		chQuit:             make(chan interface{}),
		aliveCheckInterval: 10 * time.Second,
	}
	app.getLobby(model.PlatformConsole, 0)
	app.getLobby(model.PlatformEmu, 0)
	return app
}

// getLobby returns the lobby of the platform, creating the platform's lobbies on first use.
func (a *Lbs) getLobby(platform string, lobbyID uint16) (*model.LbsLobby, bool) {
	lobbys, ok := a.lobbys[platform]
	if !ok {
		lobbys = make(map[uint16]*model.LbsLobby)
		for i := 0; i < model.LobbyCount; i++ {
			lobbys[uint16(i)] = model.NewLobby(platform, uint16(i))
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
		Platform: model.PlatformConsole,
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

// SetBattleServer gives the lobby the battle logic it hands battle users to. Call it before Serve.
func (a *Lbs) SetBattleServer(b *battle.Mcs) {
	a.battleServer = b
}

func stripHost(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		zlog.Fatal("splitPort failed", zap.Error(err))
	}
	return ":" + fmt.Sprint(port)
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
				zlog.Info("eventPeerCome")
				args.peer.lastRecvTime = time.Now()
				a.peers[args.peer.conn.Address()] = args.peer
				a.OnOpen(args.peer)
			case eventPeerMessage:
				if a.peers[args.peer.conn.Address()] != args.peer {
					continue
				}
				args.peer.lastRecvTime = time.Now()
				if f, ok := a.handlers[args.msg.Command]; ok {
					f(args.peer, args.msg)
				} else {
					zlog.Error("handler not found", zap.Any("name", symbolMap[args.msg.Command]), zap.Any("msg", args.msg))
					if args.msg.Category == message.CategoryQuestion {
						args.peer.SendMessage(message.NewServerAnswer(args.msg))
					}
				}
			case eventPeerLeave:
				zlog.Info("eventPeerLeave")
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
			zlog.Info("recv timeout", zap.String("user_id", p.UserID), zap.Any("addr", p.conn.Address()))
			p.conn.conn.Close()
		} else {
			RequestLineCheck(p)
		}
	}

	for sid, battle := range a.battles {
		if now.Sub(battle.StartTime) >= battleTimeout {
			delete(a.battles, sid)
			zlog.Info("battle user timeout", zap.Any("session_id", sid), zap.Any("battle", battle))
		}
	}

}
