package lobby

import (
	"net"
	"time"

	"zdxsv/pkg/proto"

	"github.com/golang/glog"
	pb "github.com/golang/protobuf/proto"
)

// UDPTestUserID in a Ping to the main STUN socket asks for the reachability test:
// the Pong is also sent from the test socket, which the client never sent to, so it
// only arrives when the client's port takes packets from any source (port forwarding,
// UPnP, full/address-restricted cone NAT).
const UDPTestUserID = "udptest"

// ServeUDPStunServer answers Ping with Pong (the sender's public address) on addr, and
// on addr's port + 1 (the test socket). Clients compare both public addresses to tell
// a cone NAT (same mapped port) from a symmetric one.
func (s *Server) ServeUDPStunServer(addr string) error {
	glog.Infoln("Start UDPStun", addr)
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	testAddr := &net.UDPAddr{IP: udpAddr.IP, Port: udpAddr.Port + 1}
	test, err := net.ListenUDP("udp4", testAddr)
	if err != nil {
		glog.Errorln("UDPStun test socket:", err)
		test = nil
	} else {
		glog.Infoln("Start UDPStun test", testAddr)
		defer test.Close()
		go serveUDPStun(test, nil, nil)
	}
	go Spectators.Serve(conn)
	serveUDPStun(conn, test, Spectators)
	return nil
}

// serveUDPStun answers Pings and hands spectator packets to live (nil: none).
func serveUDPStun(conn, test *net.UDPConn, live *SpectatorRegistry) {
	buf := make([]byte, 64<<10)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			glog.Errorln(err)
			if ne, ok := err.(net.Error); ok && !ne.Timeout() && !ne.Temporary() {
				return
			}
			continue
		}
		req := new(proto.Packet)
		if err := pb.Unmarshal(buf[:n], req); err != nil {
			glog.Errorln(err)
			continue
		}
		if live != nil && live.Handle(req, addr, time.Now()) {
			continue
		}
		data, test2 := udpStunAnswer(req, addr)
		if data == nil {
			continue
		}
		conn.WriteToUDP(data, addr)
		if test2 && test != nil {
			test.WriteToUDP(data, addr)
		}
	}
}

// udpStunAnswer returns the Pong for a Ping packet (nil for anything else), and
// whether the Ping asked for the reachability test.
func udpStunAnswer(req *proto.Packet, addr *net.UDPAddr) ([]byte, bool) {
	if req.GetType() != proto.MessageType_Ping {
		glog.Warningln("unexpected packet received", req)
		return nil, false
	}
	res := &proto.Packet{
		Type: proto.MessageType_Pong.Enum(),
		PongData: &proto.PongMessage{
			PublicAddr: pb.String(addr.String()),
			UserId:     pb.String("SERVER"),
			Timestamp:  pb.Int64(req.GetPingData().GetTimestamp()),
		},
	}
	data, err := pb.Marshal(res)
	if err != nil {
		glog.Errorln(err)
		return nil, false
	}
	return data, req.GetPingData().GetUserId() == UDPTestUserID
}
