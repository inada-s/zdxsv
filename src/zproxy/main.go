package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"zdxsv/pkg/lobby/lobbyrpc"
	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
)

const protocolVersion = 1006

func isSameAddr(a, b *net.UDPAddr) bool {
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

func isPS2FirstData(data []byte) bool {
	return 0 < len(data) && data[0] == 130
}

func main() {
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
	udpcl *proto.UDPClient

	selfUDPAddrs []string
	selfLocalIP  net.IP

	testBattle bool
	userID     string
	sessionID  string
	svAddr     *net.UDPAddr
	p2pAddr    map[string]*net.UDPAddr // userID -> addr
	otherIDs   []string
}

func NewZproxy() *Zproxy {
	return &Zproxy{}
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

	tmpConn, err := net.DialTimeout("tcp4", "google.com:80", time.Second)
	if err != nil {
		log.Println("ローカルIPアドレスの取得に失敗しました")
		return false
	}
	tcpAddr, ok := tmpConn.LocalAddr().(*net.TCPAddr)
	tmpConn.Close()
	if !ok {
		log.Println("ローカルIPアドレスの取得に失敗しました")
		return false
	}
	z.selfLocalIP = tcpAddr.IP
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

	z.udpcl = proto.NewUDPClient(udpConn)
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

	if conf.LobbyRPCAddr == z.selfLocalIP.String() {
		log.Println("ローカルモード使用します")
	}
	svAddr, err := net.ResolveUDPAddr("udp4", conf.LobbyRPCAddr)
	for i := 0; i < 5; i++ {
		if stunok.Load().(bool) {
			break
		}
		pkt := proto.GetPacket()
		pkt.Type = proto.MessageType_Ping.Enum()
		pkt.PingData = &proto.PingMessage{Timestamp: pb.Int64(time.Now().UnixNano())}
		z.udpcl.SendPacketTo(pkt, svAddr)
		proto.PutPacket(pkt)
		time.Sleep(100 * time.Millisecond)
	}

	if publicAddr == "" {
		log.Println("UDPアドレスの取得に失敗しました.")
		return false
	}

	z.selfUDPAddrs = append(z.selfUDPAddrs, publicAddr)
	z.selfUDPAddrs = append(z.selfUDPAddrs, fmt.Sprint(z.selfLocalIP, ":", conf.UDPListenPort))
	log.Println("UDPアドレス:", z.selfUDPAddrs)

	setupLobbyRPC()
	go addUDPPortMapping(z.selfLocalIP.String(), conf.UDPListenPort)
	return true
}

func (z *Zproxy) PollLobby() error {
	var (
		lobbyCheckDelay time.Duration = 1 * time.Millisecond

		prevMessage string

		mtx          sync.Mutex
		lobbyUsers   []lobbyrpc.User
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
				z.udpcl.SendPingToAddr(z.userID, addr)
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
				z.udpcl.SendPongTo(pkt, myUserID, addr)
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

		resp, err := registerProxy(&lobbyrpc.RegisterProxyRequest{
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
		resp, err := getBattleInfo(&lobbyrpc.BattleInfoRequest{SessionID: z.sessionID})
		if err != nil {
			return err
		}
		if !resp.Result {
			return fmt.Errorf(resp.Message)
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
				svAddrStr = fmt.Sprintf(":%d", resp.Port) // for local testing
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
	var result atomic.Value
	result.Store(false)

	defer z.udpcl.Unsubscribe("greet")
	z.udpcl.SubscribePacket("greet", func(pkt *proto.Packet, from *net.UDPAddr) {
		if pkt.GetType() == proto.MessageType_HelloServer {
			z.svAddr = from
			serverHello := pkt.GetHelloServerData()
			if serverHello != nil && serverHello.GetOk() {
				result.Store(true)
			} else {
				log.Println("Received ServerHello but not ok")
			}
		}
	})

	pkt := proto.GetPacket()
	pkt.Type = proto.MessageType_HelloServer.Enum()
	pkt.HelloServerData = &proto.HelloServerMessage{SessionId: pb.String(z.sessionID)}
	svAddr := z.svAddr
	for i := 0; i < 10; i++ {
		if !result.Load().(bool) {
			z.udpcl.SendPacketTo(pkt, svAddr)
			time.Sleep(100 * time.Millisecond)
		}
	}
	proto.PutPacket(pkt)
	return result.Load().(bool)
}

func (z *Zproxy) ServeBattle() error {
	firstData, _ := hex.DecodeString("280110310000000100ffffff")
	msgFilter := proto.NewMessageFilter(z.otherIDs)
	svRudp := proto.NewBattleBuffer("server")
	p2pRudp := map[string]*proto.BattleBuffer{}
	for id, addr := range z.p2pAddr {
		p2pRudp[addr.String()] = proto.NewBattleBuffer(id)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if z.testBattle {
		log.Println("テスト対戦を開始します")
		z.ps2cl.Write(firstData)
		z.ps2cl.Serve(ctx, func(data []byte) {
			if len(data) == 4 &&
				data[0] == 0x04 &&
				data[1] == 0xF0 &&
				data[2] == 0x00 &&
				data[3] == 0x00 {
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

	chFlush := make(chan struct{}, 1)

	defer z.udpcl.Unsubscribe("battle")
	z.udpcl.SubscribePacket("battle", func(pkt *proto.Packet, addr *net.UDPAddr) {
		switch pkt.GetType() {
		case proto.MessageType_Battle:
			if isSameAddr(addr, z.svAddr) {
				svRudp.ApplySeqAck(pkt.GetSeq(), pkt.GetAck())
			} else if rudpBuf, ok := p2pRudp[addr.String()]; ok {
				rudpBuf.ApplySeqAck(pkt.GetSeq(), pkt.GetAck())
			}
			for _, msg := range pkt.GetBattleData() {
				if msgFilter.Filter(msg) {
					z.ps2cl.Write(msg.GetBody())
				}
			}
		}
	})

	z.ps2cl.Write(firstData)

	go func() {
		z.ps2cl.Serve(ctx, func(data []byte) {
			msg := msgFilter.GenerateMessage(z.userID, data)
			svRudp.PushBattleMessage(msg)
			if !isPS2FirstData(data) {
				for _, rudpBuf := range p2pRudp {
					rudpBuf.PushBattleMessage(msg)
				}
			}
			select {
			case chFlush <- struct{}{}:
			default:
			}
			if len(data) == 4 && data[0] == 0x04 && data[1] == 0xF0 && data[2] == 0x00 && data[3] == 0x00 {
				log.Println("対戦の終了を検出しました")
				z.ps2cl.Close()
			}
		})
		cancel()
	}()

	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	lastSend := time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if time.Since(lastSend).Seconds() >= 0.030 {
				select {
				case chFlush <- struct{}{}:
				default:
				}
			}
		case <-chFlush:
			lastSend = time.Now()
			pkt := proto.GetPacket()
			{
				data, seq, ack := svRudp.GetSendData()
				pkt.Type = proto.MessageType_Battle.Enum()
				pkt.BattleData = data
				pkt.Seq = pb.Uint32(seq)
				pkt.Ack = pb.Uint32(ack)
				z.udpcl.SendPacketTo(pkt, z.svAddr)
			}
			for _, rudpBuf := range p2pRudp {
				data, seq, ack := rudpBuf.GetSendData()
				pkt.Type = proto.MessageType_Battle.Enum()
				pkt.BattleData = data
				pkt.Seq = pb.Uint32(seq)
				pkt.Ack = pb.Uint32(ack)
				addr, ok := z.p2pAddr[rudpBuf.GetID()]
				if !ok {
					log.Fatalln("p2pAddr remote not found")
				}
				z.udpcl.SendPacketTo(pkt, addr)
			}
			proto.PutPacket(pkt)
		}
	}
}
