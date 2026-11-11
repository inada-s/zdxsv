package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"zdxsv/pkg/battle"
	"zdxsv/pkg/lobby/lobbyapi"
	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
)

const protocolVersion = 1007

func isSameAddr(a, b *net.UDPAddr) bool {
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

func isPS2FirstData(data []byte) bool {
	return 0 < len(data) && data[0] == 130
}

func main() {
	loadConfig()
	log.Println("===========================================================")
	log.Printf("zproxy - ガンダムvs.Zガンダム RUDP-Proxy (v%v, ver.%v)\n", releaseVersion, protocolVersion)
	log.Println("===========================================================")
	log.Println("初めて使用する場合, 必ず接続テスト対戦を行ってください.")
	log.Println("ケネディポートの自動選抜に入ることでテスト対戦を開始します.")
	log.Println("PCがスリープしないように設定をお願いします.")
	log.Println("対戦中はソフトを終了しないでください.")

	if conf.CheckUpdate {
		printReleaseInfo()
		doSelfUpdate()
	}

	if conf.ProfileLevel >= 1 {
		log.Println("Enable pprof")
		go func() {
			log.Println(http.ListenAndServe(":16060", nil))
		}()
		if conf.ProfileLevel >= 2 {
			runtime.MemProfileRate = 1
			runtime.SetBlockProfileRate(1)
		}
	}

	app := NewZproxy()
	if !app.Setup() {
		log.Println("終了します")
		time.Sleep(5 * time.Second)
		return
	}
	for {
		app.Reset()
		err := app.PollLobby()
		if err != nil {
			log.Println("Error on PollLobby()", err)
			time.Sleep(10 * time.Second)
			continue
		}

		err = app.ServeBattle()
		if err != nil {
			log.Println("Error on ServeBattle()", err)
			time.Sleep(10 * time.Second)
			continue
		}
	}
}

type pingResult struct {
	rttNano  int64
	recvTime time.Time
	addr     *net.UDPAddr
	userID   string
}

type Zproxy struct {
	ps2sv *PS2Server
	ps2cl *PS2Conn
	udpcl *UDPClient

	selfUDPAddrs []string
	selfLocalIP  net.IP

	testBattle bool
	userID     string
	sessionID  string
	svAddr     *net.UDPAddr
	p2pAddr    map[string]*net.UDPAddr // userID -> addr
	otherIDs   []string

	localIPProbeAddr string // TCP address dialed to learn the LAN IP
}

func NewZproxy() *Zproxy {
	return &Zproxy{localIPProbeAddr: "google.com:80"}
}

// localIP returns the local address of a TCP connection to probeAddr: the IP
// of the interface that routes out.
func localIP(probeAddr string) (net.IP, error) {
	conn, err := net.DialTimeout("tcp4", probeAddr, time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		return nil, fmt.Errorf("local address %v", conn.LocalAddr())
	}
	return tcpAddr.IP, nil
}

func (z *Zproxy) Reset() {
	z.ps2cl = nil

	z.testBattle = false
	z.userID = ""
	z.sessionID = ""

	z.svAddr = nil
	z.p2pAddr = nil
	z.otherIDs = nil
}

func (z *Zproxy) Setup() bool {
	z.ps2sv = NewPS2Server()
	go func() {
		err := z.ps2sv.Listen(fmt.Sprintf(":%d", conf.TCPListenPort))
		if err != nil {
			log.Fatalln("サーバを立ち上げられませんでした", err)
		}
	}()

	ip, err := localIP(z.localIPProbeAddr)
	if err != nil {
		log.Println("ローカルIPアドレスの取得に失敗しました", err)
		return false
	}
	z.selfLocalIP = ip
	log.Println("ローカルIP:", z.selfLocalIP.String())

	tmpAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", conf.UDPListenPort))
	if err != nil {
		log.Println("UDPアドレスの取得に失敗しました", err)
		return false
	}
	udpConn, err := net.ListenUDP("udp4", tmpAddr)
	if err != nil {
		log.Println("UDPアドレスの取得に失敗しました", err)
		return false
	}

	var stunok atomic.Value
	var publicAddr string
	stunok.Store(false)

	z.udpcl = NewUDPClient(udpConn)
	go z.udpcl.ReadLoop(context.Background())

	defer z.udpcl.Unsubscribe("stun")
	z.udpcl.SubscribePacket("stun", func(pkt *proto.Packet, addr *net.UDPAddr) {
		switch pkt.GetType() {
		case proto.MessageType_Pong:
			pong := pkt.GetPongData()
			if pong.GetUserId() == "SERVER" {
				nanoRtt := time.Now().UnixNano() - pong.GetTimestamp()
				publicAddr = pong.GetPublicAddr()
				log.Println("Server RTT:", nanoRtt/(1000*1000))
				stunok.Store(true)
			}
		default:
			log.Println("unexpected packet received", pkt)
		}
	})

	if conf.StunAddr == z.selfLocalIP.String() {
		log.Println("ローカルモード使用します")
	}
	svAddr, err := net.ResolveUDPAddr("udp4", conf.StunAddr)
	for i := 0; i < 5; i++ {
		if stunok.Load().(bool) {
			break
		}
		z.udpcl.SendPacket(&proto.Packet{
			Type:     proto.MessageType_Ping.Enum(),
			PingData: &proto.PingMessage{Timestamp: pb.Int64(time.Now().UnixNano())},
		}, svAddr)
		time.Sleep(100 * time.Millisecond)
	}

	if publicAddr == "" {
		log.Println("UDPアドレスの取得に失敗しました.")
		return false
	}

	z.selfUDPAddrs = append(z.selfUDPAddrs, publicAddr)
	z.selfUDPAddrs = append(z.selfUDPAddrs, fmt.Sprint(z.selfLocalIP, ":", conf.UDPListenPort))
	log.Println("UDPアドレス:", z.selfUDPAddrs)

	if conf.EnableUPnP {
		go addUDPPortMapping(z.selfLocalIP.String(), conf.UDPListenPort)
	}
	return true
}

func (z *Zproxy) PollLobby() error {
	var (
		lobbyCheckDelay time.Duration = 1 * time.Millisecond

		prevMessage string

		mtx          sync.Mutex
		lobbyUsers   []lobbyapi.User
		pongReceived = make(map[string]pingResult)
	)

	sendPingToLobbyUsers := func() {
		mtx.Lock()
		defer mtx.Unlock()
		for _, user := range lobbyUsers {
			if user.UserID == z.userID {
				continue
			}
			for _, addr := range user.UDPAddrs {
				z.udpcl.SendPing(z.userID, addr)
			}
		}
	}

	defer z.udpcl.Unsubscribe("poll_lobby")
	z.udpcl.SubscribePacket("poll_lobby", func(pkt *proto.Packet, addr *net.UDPAddr) {
		switch pkt.GetType() {
		case proto.MessageType_Ping:
			userID := pkt.GetPingData().GetUserId()
			log.Println("Ping received from", userID)
			if conf.Verbose {
				log.Println(addr)
			}

			mtx.Lock()
			myUserID := z.userID
			mtx.Unlock()

			if userID != myUserID {
				z.udpcl.SendPong(pkt, myUserID, addr)
			}
		case proto.MessageType_Pong:
			rttNano := time.Now().UnixNano() - pkt.GetPongData().GetTimestamp()
			userID := pkt.GetPongData().GetUserId()
			log.Println("Pong received from", userID, "RTT:", rttNano/(1000*1000), "[ms]")
			if conf.Verbose {
				log.Println(addr)
			}

			mtx.Lock()
			myUserID := z.userID
			mtx.Unlock()

			if userID != myUserID {
				mtx.Lock()
				pongReceived[userID] = pingResult{
					rttNano:  rttNano,
					recvTime: time.Now(),
					addr:     addr,
					userID:   userID,
				}
				mtx.Unlock()
			}
		}
	})

	register := func() error {
		mtx.Lock()
		p2pConnected := map[string]struct{}{}
		for _, info := range pongReceived {
			if time.Since(info.recvTime).Seconds() < 10 {
				p2pConnected[info.addr.String()] = struct{}{}
			}
		}
		mtx.Unlock()

		resp, err := registerProxy(&lobbyapi.RegisterProxyRequest{
			CurrentVersion: protocolVersion,
			UserID:         conf.RegisterUserID,
			Port:           conf.TCPListenPort,
			LocalIP:        z.selfLocalIP,
			UDPAddrs:       z.selfUDPAddrs,
			P2PConnected:   p2pConnected,
		})
		if err != nil {
			return err
		}
		if prevMessage != resp.Message {
			log.Println("サーバー:", resp.Message)
		}
		prevMessage = resp.Message
		if resp.Result {
			log.Println("ロビーユーザ")
			for _, u := range resp.LobbyUsers {
				pro := "(TCP)"
				if u.UDP {
					pro = "(UDP)"
				}
				log.Println(u.UserID, u.Name, pro)
				if conf.Verbose {
					log.Println(u.UDPAddrs)
				}
			}
			mtx.Lock()
			z.sessionID = resp.SessionID
			z.userID = resp.UserID
			lobbyUsers = resp.LobbyUsers
			mtx.Unlock()
			log.Println("あなたのユーザID:", resp.UserID)
			sendPingToLobbyUsers()
		}
		return nil
	}

	prepareBattle := func() error {
		resp, err := getBattleInfo(&lobbyapi.BattleInfoRequest{SessionID: z.sessionID})
		if err != nil {
			return err
		}
		if !resp.Result {
			return errors.New(resp.Message)
		}

		z.otherIDs = nil
		for _, u := range resp.Users {
			z.otherIDs = append(z.otherIDs, u.UserID)
		}

		log.Println(resp.Message)

		z.testBattle = resp.IsTest
		if !resp.IsTest {
			svAddrStr := fmt.Sprintf("%s:%d", resp.BattleIP.String(), resp.Port)
			if z.selfLocalIP.String() == resp.BattleIP.String() {
				// for local testing; not ":port": Windows refuses to send to 0.0.0.0
				svAddrStr = fmt.Sprintf("127.0.0.1:%d", resp.Port)
			}
			svAddr, err := net.ResolveUDPAddr("udp4", svAddrStr)
			if err != nil {
				return err
			}
			z.svAddr = svAddr

			pingResults := map[string]pingResult{}
			mtx.Lock()
			for _, info := range pongReceived {
				if time.Since(info.recvTime).Seconds() < 60 {
					pingResults[info.userID] = info
				}
			}
			mtx.Unlock()

			z.p2pAddr = map[string]*net.UDPAddr{}
			for _, u := range resp.Users {
				info, ok := pingResults[u.UserID]
				if ok {
					log.Println("P2P Mode Enabled", u.UserID)
					z.p2pAddr[u.UserID] = info.addr
				}
			}
		}
		return nil
	}

	for {
		select {
		case <-time.After(lobbyCheckDelay):
			lobbyCheckDelay = 5 * time.Second
			err := register()
			if err != nil {
				return err
			}
		case ps2cl, ok := <-z.ps2sv.Accept():
			if !ok {
				return fmt.Errorf("PS2Server closed")
			}
			log.Println("PS2との接続に成功しました")
			z.ps2cl = ps2cl
			return prepareBattle()
		}
	}
}

func (z *Zproxy) GreetBattleServer() bool {
	answer := make(chan *net.UDPAddr, 1)
	defer z.udpcl.Unsubscribe("greet")
	z.udpcl.SubscribeBattle("greet", func(pkt battle.Packet, from *net.UDPAddr) {
		if pkt.Kind != battle.KindHelloAnswer {
			return
		}
		if !pkt.OK {
			log.Println("Received ServerHello but not ok")
			return
		}
		select {
		case answer <- from:
		default:
		}
	})

	hello := battle.AppendHello(nil, z.sessionID)
	for i := 0; i < 10; i++ {
		z.udpcl.Send(hello, z.svAddr)
		select {
		case from := <-answer:
			z.svAddr = from
			return true
		case <-time.After(100 * time.Millisecond):
		}
	}
	return false
}

type battlePeer struct {
	addr *net.UDPAddr
	buf  *battle.BattleBuffer
}

func (z *Zproxy) ServeBattle() error {
	firstData, _ := hex.DecodeString("280110310000000100ffffff")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if z.testBattle {
		log.Println("テスト対戦を開始します")
		z.ps2cl.Write(firstData)
		var fin FinScanner
		z.ps2cl.Serve(ctx, func(data []byte) {
			if fin.Scan(data) {
				log.Println("対戦の終了を検出しました")
				z.ps2cl.Close()
			}
		})
		return nil
	}

	log.Println("UDP通信を開始します")
	if !z.GreetBattleServer() {
		log.Println("対戦サーバとの接続に失敗しました.")
		return fmt.Errorf("Failed to greet battle server")
	}

	msgFilter := battle.NewMessageFilter(z.otherIDs)
	server := battlePeer{z.svAddr, battle.NewBattleBuffer("server")}
	var p2p []battlePeer
	for id, addr := range z.p2pAddr {
		p2p = append(p2p, battlePeer{addr, battle.NewBattleBuffer(id)})
	}
	chFlush := make(chan struct{}, 1)
	flush := func() {
		select {
		case chFlush <- struct{}{}:
		default:
		}
	}

	defer z.udpcl.Unsubscribe("battle")
	z.udpcl.SubscribeBattle("battle", func(pkt battle.Packet, addr *net.UDPAddr) {
		if pkt.Kind != battle.KindBattle {
			return
		}
		if isSameAddr(addr, server.addr) {
			server.buf.ApplySeqAck(pkt.Seq, pkt.Ack)
		} else {
			for _, p := range p2p {
				if isSameAddr(addr, p.addr) {
					p.buf.ApplySeqAck(pkt.Seq, pkt.Ack)
				}
			}
		}
		for _, msg := range pkt.Messages {
			if msgFilter.Filter(msg) {
				z.ps2cl.Write(msg.Body)
			}
		}
	})

	z.ps2cl.Write(firstData)

	go func() {
		var fin FinScanner
		z.ps2cl.Serve(ctx, func(data []byte) {
			msg := msgFilter.GenerateMessage(z.userID, data)
			server.buf.Push(msg)
			if !isPS2FirstData(data) {
				for _, p := range p2p {
					p.buf.Push(msg)
				}
			}
			flush()
			if fin.Scan(data) {
				log.Println("対戦の終了を検出しました")
				z.ps2cl.Close()
			}
		})
		cancel()
	}()

	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	lastSend := time.Now()
	var pkt []byte

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if time.Since(lastSend) >= 30*time.Millisecond {
				flush()
			}
		case <-chFlush:
			lastSend = time.Now()
			for _, p := range append([]battlePeer{server}, p2p...) {
				pkt = p.buf.AppendPacket(pkt[:0])
				z.udpcl.Send(pkt, p.addr)
			}
		}
	}
}
