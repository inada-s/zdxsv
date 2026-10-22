// Package router splits the TLS connections on one port (443) between the DNAS
// front end and an https server. It does not route by SNI: the PS2 sends an
// SSLv2-format hello, so a game console is told from the first 5 bytes.
//
// The Conn type and the proxy function are ported from google/tcpproxy
// (Apache License 2.0, Copyright 2016 Google Inc.).
package router

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// A Router decides where each accepted connection goes.
type Router struct {
	// Console serves a game console connection in this process (the DNAS server's
	// ServeConn). When nil the connection is proxied to DNASBackend.
	Console func(net.Conn)
	// DNASBackend is a DNAS server (host:port) for game consoles when Console is nil.
	DNASBackend string
	// HTTPSBackend gets every other connection, after a PROXY protocol v1 line with the
	// client's address. When empty those connections are refused with a TLS alert.
	HTTPSBackend string
	// HelloTimeout is how long to wait for the first bytes (default 3s).
	HelloTimeout time.Duration
}

func (r *Router) helloTimeout() time.Duration {
	if r.HelloTimeout == 0 {
		return 3 * time.Second
	}
	return r.HelloTimeout
}

// ListenAndServe listens on addr and serves until an accept error.
func (r *Router) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("create listener: %s", err)
	}
	return r.Serve(l)
}

// Serve accepts connections on l and routes each one.
func (r *Router) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return fmt.Errorf("accept new conn: %s", err)
		}
		conn := &Conn{Conn: c, router: r}
		go conn.proxy()
	}
}

// IsGameConsoleHello reports whether the connection starts like the PS2's
// SSLv2-format client hello (TLS 1.0, 769 bytes).
func IsGameConsoleHello(r io.Reader) bool {
	var hdr struct {
		Type         uint8
		Major, Minor uint8
		Length       uint16
	}

	if err := binary.Read(r, binary.BigEndian, &hdr); err != nil {
		return false
	}

	log.Println("hdr:", hdr)

	// FIXME: more strict validation.
	return hdr.Type == 128 && hdr.Major == 100 && hdr.Minor == 1 && hdr.Length == 769
}

// A Conn handles the proxying of one user connection.
type Conn struct {
	net.Conn

	router   *Router
	backend  string
	tlsMinor int
}

func (c *Conn) logf(msg string, args ...interface{}) {
	msg = fmt.Sprintf(msg, args...)
	log.Printf("%s <> %s: %s", c.RemoteAddr(), c.LocalAddr(), msg)
}

func (c *Conn) abort(alert byte, msg string, args ...interface{}) {
	c.logf(msg, args...)
	alertMsg := []byte{21, 3, byte(c.tlsMinor), 0, 2, 2, alert}

	if err := c.SetWriteDeadline(time.Now().Add(c.router.helloTimeout())); err != nil {
		c.logf("error while setting write deadline during abort: %s", err)
		// Do NOT send the alert if we can't set a write deadline,
		// that could result in leaking a connection for an extended
		// period.
		return
	}

	if _, err := c.Write(alertMsg); err != nil {
		c.logf("error while sending alert: %s", err)
	}
}

func (c *Conn) internalError(msg string, args ...interface{}) {
	c.abort(80, msg, args...)
}

func (c *Conn) proxy() {
	defer c.Close()

	if err := c.SetReadDeadline(time.Now().Add(c.router.helloTimeout())); err != nil {
		c.internalError("Setting read deadline for ClientHello: %s", err)
		return
	}

	var tmpBuf bytes.Buffer
	isGameConsole := IsGameConsoleHello(io.TeeReader(c.Conn, &tmpBuf))

	if err := c.SetReadDeadline(time.Time{}); err != nil {
		c.internalError("Clearing read deadline for ClientHello: %s", err)
		return
	}

	if isGameConsole && c.router.Console != nil {
		// Hand the connection over with the sniffed bytes put back in front.
		c.router.Console(&prefixConn{Conn: c.Conn, r: io.MultiReader(&tmpBuf, c.Conn)})
		return
	}

	c.backend = c.router.HTTPSBackend
	if isGameConsole {
		c.backend = c.router.DNASBackend
	}
	if c.backend == "" {
		c.internalError("no backend for this connection (console: %v)", isGameConsole)
		return
	}

	backend, err := net.DialTimeout("tcp", c.backend, 10*time.Second)
	if err != nil {
		c.internalError("failed to dial backend %q: %s", c.backend, err)
		return
	}
	defer backend.Close()

	if !isGameConsole {
		remote := c.RemoteAddr().(*net.TCPAddr)
		local := c.LocalAddr().(*net.TCPAddr)
		family := "TCP6"
		if remote.IP.To4() != nil {
			family = "TCP4"
		}
		if _, err := fmt.Fprintf(backend, "PROXY %s %s %s %d %d\r\n", family, remote.IP, local.IP, remote.Port, local.Port); err != nil {
			c.internalError("failed to send PROXY header to %q: %s", c.backend, err)
			return
		}
	}

	// Replay the piece of the handshake we had to read to do the
	// routing, then blindly proxy any other bytes.
	n, err := io.Copy(backend, &tmpBuf)
	log.Println("written ", n, "bytes")
	if err != nil {
		c.internalError("failed to replay handshake to %q: %s", c.backend, err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go proxy(&wg, c.Conn, backend)
	go proxy(&wg, backend, c.Conn)
	wg.Wait()
}

// prefixConn is a connection whose first reads return bytes already taken from it.
type prefixConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.r.Read(p) }

type halfCloser interface {
	CloseWrite() error
	CloseRead() error
}

// proxy copies b to a until b's peer closes its write side, then passes that
// half-close on to a's peer.
func proxy(wg *sync.WaitGroup, a, b net.Conn) {
	defer wg.Done()
	if _, err := io.Copy(a, b); err != nil {
		log.Printf("%s<>%s -> %s<>%s: %s", a.RemoteAddr(), a.LocalAddr(), b.LocalAddr(), b.RemoteAddr(), err)
	}
	if h, ok := a.(halfCloser); ok {
		h.CloseWrite()
	}
	if h, ok := b.(halfCloser); ok {
		h.CloseRead()
	}
}
