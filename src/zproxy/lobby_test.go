package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"zdxsv/pkg/lobby"
	"zdxsv/pkg/lobby/lobbyapi"
	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
)

// freePort reserves a loopback TCP port and frees it for a listener that takes an address.
func freePort(t *testing.T) *net.TCPAddr {
	t.Helper()
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr)
}

// fakeLobby is the lobby API zproxy talks to, answering with battle and keeping the requests.
type fakeLobby struct {
	registers chan *lobbyapi.RegisterProxyRequest
	infos     chan *lobbyapi.BattleInfoRequest
	conns     atomic.Int32
}

// useFakeLobby points zproxy's lobby API client at a local fake lobby.
func useFakeLobby(t *testing.T, register *lobbyapi.RegisterProxyResponse, battle *lobbyapi.BattleInfoResponse) *fakeLobby {
	t.Helper()
	l := &fakeLobby{
		registers: make(chan *lobbyapi.RegisterProxyRequest, 10),
		infos:     make(chan *lobbyapi.BattleInfoRequest, 10),
	}
	sv := httptest.NewUnstartedServer(&lobby.APIHandler{
		RegisterProxy: func(_ string, r *lobbyapi.RegisterProxyRequest) *lobbyapi.RegisterProxyResponse {
			l.registers <- r
			return register
		},
		BattleInfo: func(r *lobbyapi.BattleInfoRequest) *lobbyapi.BattleInfoResponse {
			l.infos <- r
			return battle
		},
	})
	sv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			l.conns.Add(1)
		}
	}
	sv.Start()
	t.Cleanup(sv.Close)
	useLobbyAPI(t, sv.URL)
	return l
}

func useLobbyAPI(t *testing.T, url string) {
	t.Helper()
	conf.LobbyAPIURL = url
	conf.RegisterUserID = "_AUTO_"
	t.Cleanup(lobbyAPIClient.CloseIdleConnections)
}

// lobbyProxy is a zproxy as after Setup, on loopback, with its PS2 port listening.
func lobbyProxy(t *testing.T) (*Zproxy, string) {
	t.Helper()
	z := NewZproxy()
	z.udpcl, z.selfUDPAddrs = newUDPPeer(t)
	z.selfLocalIP = net.IPv4(127, 0, 0, 1).To4()
	ps2Addr := freePort(t)
	conf.TCPListenPort = uint16(ps2Addr.Port)
	z.ps2sv = NewPS2Server()
	go z.ps2sv.Listen(ps2Addr.String())
	return z, ps2Addr.String()
}

func newUDPPeer(t *testing.T) (*UDPClient, []string) {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		c.Close()
	})
	cl := NewUDPClient(c)
	go cl.ReadLoop(ctx)
	return cl, []string{c.LocalAddr().String()}
}

// pollLobby runs PollLobby; at cleanup, before the lobby stops, a PS2 connects if it has not
// returned yet: a PollLobby left running would register with the next test's lobby.
func pollLobby(t *testing.T, z *Zproxy, ps2Addr string) chan error {
	t.Helper()
	done := make(chan error, 1)
	returned := make(chan struct{})
	go func() {
		done <- z.PollLobby()
		close(returned)
	}()
	t.Cleanup(func() {
		select {
		case <-returned:
			return
		default:
		}
		if c, err := net.Dial("tcp4", ps2Addr); err == nil {
			defer c.Close()
		}
		select {
		case <-returned:
		case <-time.After(3 * time.Second):
			t.Error("PollLobby still running")
		}
	})
	return done
}

func connectPS2(t *testing.T, addr string) {
	t.Helper()
	var c net.Conn
	var err error
	for i := 0; i < 20; i++ {
		if c, err = net.Dial("tcp4", addr); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
}

// waitFor receives from ch within d.
func waitFor(t *testing.T, ch interface{}, d time.Duration, what string) interface{} {
	t.Helper()
	i, v, _ := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch)},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(time.After(d))},
	})
	if i == 1 {
		t.Fatalf("no %s in %v", what, d)
	}
	return v.Interface()
}

func waitRegister(t *testing.T, l *fakeLobby, d time.Duration) *lobbyapi.RegisterProxyRequest {
	t.Helper()
	return waitFor(t, l.registers, d, "register").(*lobbyapi.RegisterProxyRequest)
}

func waitPoll(t *testing.T, done chan error, d time.Duration) error {
	t.Helper()
	err, _ := waitFor(t, done, d, "PollLobby return").(error)
	return err
}

// A zproxy registers, pings the lobby users, reports who answered, and on the PS2's
// connection takes the battle server and the P2P addresses of the users that answered.
func TestPollLobbyBattle(t *testing.T) {
	u2, u2Addrs := newUDPPeer(t)
	u3, u3Addrs := newUDPPeer(t)
	pings := make(chan string, 10)
	u2.SubscribePacket("t", func(pkt *proto.Packet, addr *net.UDPAddr) {
		switch pkt.GetType() {
		case proto.MessageType_Ping:
			pings <- pkt.GetPingData().GetUserId()
			u2.SendPong(pkt, "U2", addr)
		}
	})
	// U3 is in the lobby but never answers
	u3.SubscribePacket("t", func(*proto.Packet, *net.UDPAddr) {})

	l := useFakeLobby(t,
		&lobbyapi.RegisterProxyResponse{Result: true, SessionID: "12345678", UserID: "U1", LobbyUsers: []lobbyapi.User{
			{UserID: "U1", UDPAddrs: []string{"127.0.0.1:1"}},
			{UserID: "U2", UDPAddrs: u2Addrs, UDP: true},
			{UserID: "U3", UDPAddrs: u3Addrs, UDP: true},
		}},
		&lobbyapi.BattleInfoResponse{Result: true, BattleIP: net.IPv4(127, 0, 0, 1), Port: 9999, Users: []lobbyapi.User{
			{UserID: "U1"}, {UserID: "U2"}, {UserID: "U3"},
		}})
	z, ps2Addr := lobbyProxy(t)
	done := pollLobby(t, z, ps2Addr)

	r := waitRegister(t, l, 2*time.Second)
	want := &lobbyapi.RegisterProxyRequest{CurrentVersion: protocolVersion, UserID: "_AUTO_",
		LocalIP: r.LocalIP, Port: conf.TCPListenPort, UDPAddrs: z.selfUDPAddrs, P2PConnected: map[string]struct{}{}}
	if !reflect.DeepEqual(r, want) || !r.LocalIP.Equal(z.selfLocalIP) {
		t.Fatalf("register %+v\nwant %+v", r, want)
	}
	if id := waitFor(t, pings, 2*time.Second, "ping to U2").(string); id != "U1" {
		t.Fatalf("ping from %q, want U1", id)
	}

	// re-registers every 5 s with the users that answered
	r = waitRegister(t, l, 7*time.Second)
	if want := map[string]struct{}{u2Addrs[0]: {}}; !reflect.DeepEqual(r.P2PConnected, want) {
		t.Fatalf("P2PConnected %v, want %v", r.P2PConnected, want)
	}

	connectPS2(t, ps2Addr)
	if err := waitPoll(t, done, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if r := waitFor(t, l.infos, time.Second, "battle info").(*lobbyapi.BattleInfoRequest); r.SessionID != "12345678" {
		t.Fatalf("battle info for %q", r.SessionID)
	}
	if n := l.conns.Load(); n != 1 {
		t.Fatalf("%d connections to the lobby API, want 1 for the registers and the battle info", n)
	}
	if z.userID != "U1" || z.sessionID != "12345678" || z.testBattle || z.ps2cl == nil {
		t.Fatalf("userID %q sessionID %q test %v ps2 %v", z.userID, z.sessionID, z.testBattle, z.ps2cl)
	}
	if z.svAddr.String() != "127.0.0.1:9999" {
		t.Fatalf("battle server %v", z.svAddr)
	}
	if !reflect.DeepEqual(z.otherIDs, []string{"U1", "U2", "U3"}) {
		t.Fatalf("otherIDs %v", z.otherIDs)
	}
	if len(z.p2pAddr) != 1 || z.p2pAddr["U2"].String() != u2Addrs[0] {
		t.Fatalf("p2pAddr %v, want only U2 %v", z.p2pAddr, u2Addrs[0])
	}
}

// A ping from another user is answered with this proxy's user id; one from itself is not.
func TestPollLobbyAnswersPing(t *testing.T) {
	l := useFakeLobby(t, &lobbyapi.RegisterProxyResponse{Result: true, SessionID: "12345678", UserID: "U1"}, nil)
	z, ps2Addr := lobbyProxy(t)
	pollLobby(t, z, ps2Addr)
	waitRegister(t, l, 2*time.Second)

	u2, _ := newUDPPeer(t)
	pongs := make(chan *proto.PongMessage, 100)
	u2.SubscribePacket("t", func(pkt *proto.Packet, _ *net.UDPAddr) {
		if pkt.GetType() == proto.MessageType_Pong {
			pongs <- pkt.GetPongData()
		}
	})
	zAddr, _ := net.ResolveUDPAddr("udp4", z.selfUDPAddrs[0])
	ping := func(userID string, ts int64) {
		u2.SendPacket(&proto.Packet{
			Type:     proto.MessageType_Ping.Enum(),
			PingData: &proto.PingMessage{Timestamp: pb.Int64(ts), UserId: pb.String(userID)},
		}, zAddr)
	}

	// Answers carry the user id once the register call has returned.
	deadline := time.After(2 * time.Second)
	for ts := int64(1); ; ts++ {
		ping("U2", ts)
		select {
		case p := <-pongs:
			if p.GetUserId() == "U1" {
				goto registered
			}
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("no pong from U1")
		}
	}
registered:
	// Pings are answered in order: an answer to the own ping comes before the next one's.
	ping("U1", -1)
	ping("U2", -2)
	for {
		p := waitFor(t, pongs, 2*time.Second, "pong").(*proto.PongMessage)
		switch p.GetTimestamp() {
		case -1:
			t.Fatalf("answered its own user id with %q", p.GetUserId())
		case -2:
			if p.GetUserId() != "U1" {
				t.Fatalf("pong from %q, want U1", p.GetUserId())
			}
			return
		}
	}
}

func TestPollLobbyBattleInfo(t *testing.T) {
	reg := &lobbyapi.RegisterProxyResponse{Result: true, SessionID: "12345678", UserID: "U1"}
	t.Run("test battle", func(t *testing.T) {
		useFakeLobby(t, reg, &lobbyapi.BattleInfoResponse{Result: true, IsTest: true, Users: []lobbyapi.User{{UserID: "U1"}}})
		z, ps2Addr := lobbyProxy(t)
		done := pollLobby(t, z, ps2Addr)
		connectPS2(t, ps2Addr)
		if err := waitPoll(t, done, 2*time.Second); err != nil {
			t.Fatal(err)
		}
		if !z.testBattle || z.svAddr != nil {
			t.Fatalf("test %v battle server %v", z.testBattle, z.svAddr)
		}
	})
	t.Run("refused", func(t *testing.T) {
		useFakeLobby(t, reg, &lobbyapi.BattleInfoResponse{Result: false, Message: "no battle"})
		z, ps2Addr := lobbyProxy(t)
		done := pollLobby(t, z, ps2Addr)
		connectPS2(t, ps2Addr)
		if err := waitPoll(t, done, 2*time.Second); err == nil || err.Error() != "no battle" {
			t.Fatalf("err %v, want no battle", err)
		}
	})
}

func TestPollLobbyUnreachable(t *testing.T) {
	useLobbyAPI(t, "http://"+freePort(t).String())
	z, ps2Addr := lobbyProxy(t)
	if err := waitPoll(t, pollLobby(t, z, ps2Addr), 3*time.Second); err == nil {
		t.Fatal("no error without a lobby")
	}
}
