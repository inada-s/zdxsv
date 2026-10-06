package lobby

import (
	"net"
	"testing"
	"time"

	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
)

func stunPing(t *testing.T, c *net.UDPConn, to *net.UDPAddr, userID string) {
	data, err := pb.Marshal(&proto.Packet{
		Type:     proto.MessageType_Ping.Enum(),
		PingData: &proto.PingMessage{Timestamp: pb.Int64(42), UserId: pb.String(userID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteToUDP(data, to); err != nil {
		t.Fatal(err)
	}
}

// stunPongs returns the source port -> Pong public_addr of what arrives within d.
func stunPongs(t *testing.T, c *net.UDPConn, d time.Duration) map[int]string {
	got := map[int]string{}
	buf := make([]byte, 4096)
	c.SetReadDeadline(time.Now().Add(d))
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return got
		}
		res := new(proto.Packet)
		if err := pb.Unmarshal(buf[:n], res); err != nil || res.GetType() != proto.MessageType_Pong || res.GetPongData().GetTimestamp() != 42 {
			t.Fatalf("bad answer %q", buf[:n])
		}
		got[from.Port] = res.GetPongData().GetPublicAddr()
	}
}

func TestUDPStunServer(t *testing.T) {
	// a free port whose port + 1 is free too
	var port int
	for i := 0; i < 20 && port == 0; i++ {
		a, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		p := a.LocalAddr().(*net.UDPAddr).Port
		a.Close()
		if b, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p + 1}); err == nil {
			b.Close()
			port = p
		}
	}
	srv := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	go (&Server{}).ServeUDPStunServer(srv.String())
	time.Sleep(200 * time.Millisecond)
	test := &net.UDPAddr{IP: srv.IP, Port: port + 1}

	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	me := c.LocalAddr().String()

	// plain Ping: one Pong, from the main socket
	stunPing(t, c, srv, "AAAAAA")
	if got := stunPongs(t, c, 500*time.Millisecond); len(got) != 1 || got[port] != me {
		t.Fatalf("plain ping: %v want only {%d:%s}", got, port, me)
	}
	// reachability test: also a Pong from the test socket, never sent to
	stunPing(t, c, srv, "udptest") // UDPTestUserID
	if got := stunPongs(t, c, 500*time.Millisecond); len(got) != 2 || got[port] != me || got[port+1] != me {
		t.Fatalf("udptest ping: %v want {%d:%s %d:%s}", got, port, me, port+1, me)
	}
	// the test socket answers Pings itself (the second mapping for the NAT type)
	stunPing(t, c, test, "AAAAAA")
	if got := stunPongs(t, c, 500*time.Millisecond); len(got) != 1 || got[port+1] != me {
		t.Fatalf("test socket ping: %v want only {%d:%s}", got, port+1, me)
	}
}
