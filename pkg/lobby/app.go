package lobby

import (
	"fmt"
	"net"
	"net/rpc"
	"time"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/config"
	"zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"

	"github.com/golang/glog"
)

type MessageHandler func(*AppPeer, *message.Message)

type handlerHolder struct {
	handlers     map[uint16]MessageHandler
	handlerNames map[uint16]string
}

var defaultHandlers = &handlerHolder{
	handlers:     make(map[uint16]MessageHandler),
	handlerNames: make(map[uint16]string),
}

func register(id uint16, name string, f MessageHandler) interface{} {
	defaultHandlers.handlers[id] = f
	defaultHandlers.handlerNames[id] = name
	return nil
}

type eventPeerCome struct {
	peer *AppPeer
}

type eventPeerLeave struct {
	peer *AppPeer
}

type eventPeerMessage struct {
	peer *AppPeer
	msg  *message.Message
}

type eventFunc struct {
	f func(*App)
	c chan<- interface{}
}

type AppPeer struct {
	model.User
	conn              *Conn
	app               *App
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

func (p *AppPeer) OnOpen() {
	p.app.chEvent <- eventPeerCome{peer: p}
}

func (p *AppPeer) OnMessage(msg *message.Message) {
	p.app.chEvent <- eventPeerMessage{peer: p, msg: msg}
}

func (p *AppPeer) OnClose() {
	p.app.chEvent <- eventPeerLeave{peer: p}
}

func (p *AppPeer) SendMessage(msg *message.Message) {
	p.conn.SendMessage(msg)
}

func (p *AppPeer) IP() string {
	h, _, err := net.SplitHostPort(p.conn.Address())
	if err != nil {
		return ""
	}
	return h
}

// BattleServer is the lobby's link to the battle server: the net/rpc client of a
// separate `zdxsv battle` process, or the battle logic itself in `zdxsv all`.
type BattleServer interface {
	PingPong(args *string, reply *string) error
	NotifyBattleUsers(args *battlerpc.BattleInfoArgs, reply *int) error
	Close() error
}

// rpcBattleServer calls a `zdxsv battle` process over net/rpc (battle.Logic.ServeRpc).
type rpcBattleServer struct{ *rpc.Client }

func (c rpcBattleServer) PingPong(args *string, reply *string) error {
	return c.Call("Logic.PingPong", args, reply)
}

func (c rpcBattleServer) NotifyBattleUsers(args *battlerpc.BattleInfoArgs, reply *int) error {
	return c.Call("Logic.NotifyBattleUsers", args, reply)
}

// localBattleServer is the battle logic in this process.
type localBattleServer struct{ *battlerpc.BattleRpc }

func (localBattleServer) Close() error { return nil }

type App struct {
	*handlerHolder
	battleServer BattleServer
	users        map[string]*AppPeer
	lobbys       map[string]map[uint16]*model.Lobby // platform -> lobby id -> lobby
	battles      map[string]*model.Battle
	peers        map[string]*AppPeer // conn address -> peer, owned by Serve
	chEvent      chan interface{}
	chQuit       chan interface{}

	aliveCheckInterval time.Duration
	relayProv          *relayProvisioning
}

const (
	peerRecvTimeout = 2 * time.Minute
	battleTimeout   = time.Hour
)

func NewApp() *App {
	app := &App{
		handlerHolder:      defaultHandlers,
		users:              make(map[string]*AppPeer),
		lobbys:             make(map[string]map[uint16]*model.Lobby),
		battles:            make(map[string]*model.Battle),
		peers:              make(map[string]*AppPeer),
		chEvent:            make(chan interface{}, 64),
		chQuit:             make(chan interface{}),
		aliveCheckInterval: 10 * time.Second,
	}
	app.getLobby(model.PlatformConsole, 0)
	app.getLobby(model.PlatformEmu, 0)
	return app
}

const lobbyCount = 26

// getLobby returns the lobby of the platform, creating the platform's lobbies on first use.
func (a *App) getLobby(platform string, lobbyID uint16) (*model.Lobby, bool) {
	lobbys, ok := a.lobbys[platform]
	if !ok {
		lobbys = make(map[uint16]*model.Lobby)
		for i := 0; i < lobbyCount; i++ {
			lobbys[uint16(i)] = model.NewLobby(platform, uint16(i))
		}
		a.lobbys[platform] = lobbys
	}
	l, ok := lobbys[lobbyID]
	return l, ok
}

func (a *App) NewPeer(conn *Conn) Peer {
	return &AppPeer{
		conn:     conn,
		app:      a,
		Platform: model.PlatformConsole,
	}
}

func (a *App) Locked(f func(*App)) {
	c := make(chan interface{})
	a.chEvent <- eventFunc{
		f: f,
		c: c,
	}
	<-c
}

func (a *App) Quit() {
	a.Locked(func(app *App) {
		for _, p := range app.users {
			SendServerShutDown(p)
		}
	})
	time.Sleep(10 * time.Millisecond)
	close(a.chQuit)
}

func (a *App) AddHandler(id uint16, name string, f MessageHandler) {
	a.handlers[id] = f
	a.handlerNames[id] = name
}

// SetBattleServer makes the lobby drive the battle logic b in this process instead of
// a `zdxsv battle` process at config.Conf.Battle.RPCAddr. Call it before Serve.
func (a *App) SetBattleServer(b *battlerpc.BattleRpc) {
	a.battleServer = localBattleServer{b}
}

func (a *App) connectToBattleServer() {
	client, err := rpc.DialHTTP("tcp", config.Conf.Battle.RPCAddr)
	if err != nil {
		glog.Errorln("Faild to connect BattleServer. ", err)
	} else {
		glog.Infoln("Succeeded to connect BattleServer")
		a.battleServer = rpcBattleServer{client}
	}
}

func stripHost(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		glog.Fatal("err in splitPort", err)
	}
	return ":" + fmt.Sprint(port)
}

func (a *App) Serve() {
	aliveCheck := time.NewTicker(a.aliveCheckInterval)
	defer aliveCheck.Stop()
	err := newLobbyRPCServer(a, stripHost(config.Conf.Lobby.RPCAddr)).Start()
	if err != nil {
		glog.Errorln(err)
	}
	if a.battleServer == nil {
		a.connectToBattleServer()
	}

	for {
		select {
		case <-a.chQuit:
			return
		case e := <-a.chEvent:
			switch args := e.(type) {
			case eventPeerCome:
				glog.Infoln("eventPeerCome")
				args.peer.lastRecvTime = time.Now()
				a.peers[args.peer.conn.Address()] = args.peer
				a.OnOpen(args.peer)
			case eventPeerMessage:
				args.peer.lastRecvTime = time.Now()
				if f, ok := a.handlers[args.msg.Command]; ok {
					f(args.peer, args.msg)
				} else {
					glog.Errorf("======================================")
					glog.Errorf("======================================")
					glog.Errorf("======================================")
					glog.Errorf("Handler not found: name = %v msg = %v", symbolMap[args.msg.Command], args.msg)
					glog.Errorf("======================================")
					glog.Errorf("======================================")
					glog.Errorf("======================================")
					if args.msg.Category == message.CategoryQuestion {
						args.peer.SendMessage(message.NewServerAnswer(args.msg))
					}
				}
			case eventPeerLeave:
				glog.Infoln("eventPeerLeave")
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
func (a *App) checkAlive(now time.Time) {
	for _, p := range a.peers {
		if now.Sub(p.lastRecvTime) >= peerRecvTimeout {
			glog.Infoln("Recv Timeout", p)
			p.conn.conn.Close()
		} else {
			RequestLineCheck(p)
		}
	}

	for sid, battle := range a.battles {
		if now.Sub(battle.StartTime) >= battleTimeout {
			delete(a.battles, sid)
			glog.Infoln("Battle user timeout.", sid, battle)
		}
	}

	if a.battleServer == nil {
		a.connectToBattleServer()
	} else {
		ping, reply := "Ping", ""
		err := a.battleServer.PingPong(&ping, &reply)
		if err != nil {
			glog.Errorln("PingPong failed. ", err)
			a.battleServer.Close()
			a.battleServer = nil
			a.connectToBattleServer()
		}
	}
}
