package main

import (
	"fmt"
	"net"
	"reflect"
	"sync/atomic"
	"testing"

	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
)

// fakeStun is the UDP server Setup pings for its public address. It answers
// with publicAddr unless silent, and counts the pings.
type fakeStun struct {
	addr  string
	pings atomic.Int32
}

func startFakeStun(t *testing.T, publicAddr string, silent bool) *fakeStun {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	s := &fakeStun{addr: c.LocalAddr().String()}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var req proto.Packet
			if pb.Unmarshal(buf[:n], &req) != nil || req.GetType() != proto.MessageType_Ping {
				continue
			}
			s.pings.Add(1)
			if silent {
				continue
			}
			data, _ := pb.Marshal(&proto.Packet{
				Type: proto.MessageType_Pong.Enum(),
				PongData: &proto.PongMessage{
					PublicAddr: pb.String(publicAddr),
					UserId:     pb.String("SERVER"),
					Timestamp:  pb.Int64(req.GetPingData().GetTimestamp()),
				},
			})
			c.WriteToUDP(data, from)
		}
	}()
	return s
}

// freeUDPPort returns a loopback UDP port free at the time of the call.
func freeUDPPort(t *testing.T) uint16 {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return uint16(c.LocalAddr().(*net.UDPAddr).Port)
}

// setupProxy runs Setup with the local IP probe at probeAddr and the lobby at stun.
func setupProxy(t *testing.T, probeAddr string, stun *fakeStun) (*Zproxy, bool) {
	t.Helper()
	conf.TCPListenPort = 0
	conf.UDPListenPort = freeUDPPort(t)
	conf.EnableUPnP = false
	conf.LobbyRPCAddr = stun.addr
	z := NewZproxy()
	z.localIPProbeAddr = probeAddr
	ok := z.Setup()
	if lobbyRPCClient != nil {
		t.Cleanup(func() {
			lobbyRPCClient.Stop()
			lobbyRPCClient = nil
		})
	}
	return z, ok
}

func TestSetup(t *testing.T) {
	probe := listenProbe(t)
	stun := startFakeStun(t, "203.0.113.5:9000", false)
	z, ok := setupProxy(t, probe, stun)
	if !ok {
		t.Fatal("Setup failed")
	}
	if !z.selfLocalIP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("local IP %v, want the probe connection's 127.0.0.1", z.selfLocalIP)
	}
	want := []string{"203.0.113.5:9000", fmt.Sprint("127.0.0.1:", conf.UDPListenPort)}
	if !reflect.DeepEqual(z.selfUDPAddrs, want) {
		t.Errorf("UDP addrs %v, want %v", z.selfUDPAddrs, want)
	}
	if lobbyRPCClient == nil || lobbyRPCClient.Addr != stun.addr {
		t.Errorf("lobby RPC client not set up for %s", stun.addr)
	}
}

func TestSetupNoLocalIP(t *testing.T) {
	stun := startFakeStun(t, "203.0.113.5:9000", false)
	if _, ok := setupProxy(t, freePort(t).String(), stun); ok {
		t.Error("Setup succeeded with the local IP probe refused")
	}
	if n := stun.pings.Load(); n != 0 {
		t.Errorf("%d pings sent without a local IP", n)
	}
}

func TestSetupNoPublicAddr(t *testing.T) {
	stun := startFakeStun(t, "", true)
	if _, ok := setupProxy(t, listenProbe(t), stun); ok {
		t.Error("Setup succeeded with no Pong from the lobby")
	}
	if n := stun.pings.Load(); n != 5 {
		t.Errorf("%d pings to a silent lobby, want 5", n)
	}
}

// listenProbe returns the address of a loopback TCP listener for the local IP probe.
func listenProbe(t *testing.T) string {
	t.Helper()
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}
