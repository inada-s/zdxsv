package lobby

import (
	"net"

	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
	"zdxsv/pkg/zlog"
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
	zlog.Infoln("Start UDPStun", addr)
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
		zlog.Errorln("UDPStun test socket:", err)
		test = nil
	} else {
		zlog.Infoln("Start UDPStun test", testAddr)
		defer test.Close()
		go serveUDPStun(test, nil)
	}

	serveUDPStun(conn, test)
	return nil
}

// ServeUDPStun answers Ping with Pong on addr, without the test socket.
func (s *Server) ServeUDPStun(addr string) error {
	zlog.Infoln("Start UDPStun", addr)
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	serveUDPStun(conn, nil)
	return nil
}

// serveUDPStun answers Pings.
func serveUDPStun(conn, test *net.UDPConn) {
	buf := make([]byte, 64<<10)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			zlog.Errorln(err)
			if ne, ok := err.(net.Error); ok && !ne.Timeout() && !ne.Temporary() {
				return
			}
			continue
		}
		req := new(proto.Packet)
		if err := pb.Unmarshal(buf[:n], req); err != nil {
			zlog.Errorln(err)
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
		zlog.Warnln("unexpected packet received", req)
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
		zlog.Errorln(err)
		return nil, false
	}
	return data, req.GetPingData().GetUserId() == UDPTestUserID
}
