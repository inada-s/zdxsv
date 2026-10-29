package battle

import (
	"errors"
	"net"
)

type TCPServer struct {
	logic *Logic
}

func NewTCPServer(logic *Logic) *TCPServer {
	return &TCPServer{
		logic: logic,
	}
}

func (s *TCPServer) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

// Serve accepts connections on l until l is closed.
func (s *TCPServer) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return err
		}
		if err != nil {
			continue
		}
		peer := NewTCPPeer(conn)
		go peer.Serve(s.logic)
	}
}
